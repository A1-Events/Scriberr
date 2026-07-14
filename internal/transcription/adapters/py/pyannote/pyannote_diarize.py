#!/usr/bin/env python3
"""
PyAnnote speaker diarization script.
Processes audio files to identify and separate different speakers.
"""

import argparse
import json
import sys
import os
from pathlib import Path
from pyannote.audio import Pipeline
import torch

# Fix for PyTorch 2.6+ which defaults weights_only=True
# We need to allowlist PyAnnote's custom classes
try:
    from pyannote.audio.core.task import Specifications, Problem, Resolution
    if hasattr(torch.serialization, "add_safe_globals"):
        torch.serialization.add_safe_globals([Specifications, Problem, Resolution])
except ImportError:
    pass
except Exception as e:
    print(f"Warning: Could not add safe globals: {e}")


def diarize_audio(
    audio_path: str,
    output_file: str,
    hf_token: str,
    model: str = "pyannote/speaker-diarization-community-1",
    min_speakers: int = None,
    max_speakers: int = None,
    output_format: str = "rttm",
    device: str = "auto",
    segmentation_onset: float = None,
    segmentation_offset: float = None,
    include_embeddings: bool = False,
):
    """
    Perform speaker diarization on audio file using PyAnnote.
    """
    print(f"Loading PyAnnote speaker diarization pipeline: {model}")

    try:
        # Initialize the diarization pipeline
        pipeline = Pipeline.from_pretrained(
            model,
            token=hf_token
        )

        # Move to specified device
        # if device == "auto" or device == "cuda":
        try:
            if torch.cuda.is_available():
                pipeline = pipeline.to(torch.device("cuda"))
                print("Using CUDA for diarization")
            elif device == "cuda":
                print("CUDA requested but not available, falling back to CPU")
            else:
                print("CUDA not available, using CPU")
        except ImportError:
            print("PyTorch not available for CUDA, using CPU")
        except Exception as e:
            print(f"Error moving to device: {e}, using CPU")

        # Apply segmentation thresholds if provided
        if segmentation_onset is not None or segmentation_offset is not None:
            try:
                # Get current parameters
                params = pipeline.parameters(instantiated=True)

                # Update segmentation thresholds
                if "segmentation" in params:
                    if segmentation_onset is not None:
                        params["segmentation"]["threshold"] = segmentation_onset
                        print(f"Set segmentation onset threshold: {segmentation_onset}")
                    if segmentation_offset is not None:
                        # PyAnnote uses min_duration_off for offset behavior
                        params["segmentation"]["min_duration_off"] = segmentation_offset
                        print(f"Set segmentation offset (min_duration_off): {segmentation_offset}")

                    # Instantiate pipeline with new parameters
                    pipeline.instantiate(params)
                else:
                    print("Warning: Could not find segmentation parameters in pipeline")
            except Exception as e:
                print(f"Warning: Could not set segmentation thresholds: {e}")
                print("Continuing with default thresholds")

        print("Pipeline loaded successfully")
    except Exception as e:
        print(f"Error loading pipeline: {e}")
        print("Make sure you have a valid Hugging Face token and have accepted the model's license")
        sys.exit(1)

    print(f"Processing audio file: {audio_path}")

    try:
        # Run diarization
        diarization_params = {}
        if min_speakers is not None:
            diarization_params["min_speakers"] = min_speakers
        if max_speakers is not None:
            diarization_params["max_speakers"] = max_speakers

        if include_embeddings:
            diarization_params["return_embeddings"] = True

        if diarization_params:
            print(f"Using diarization params: {sorted(diarization_params)}")
            result = pipeline(audio_path, **diarization_params)
        else:
            print("Using automatic speaker detection")
            result = pipeline(audio_path)

        # With return_embeddings=True, pyannote 3.x returns (annotation, matrix);
        # pyannote 4.x returns an output object carrying an `embeddings` attribute.
        embeddings_map = None
        diarization = result
        if include_embeddings:
            if isinstance(result, tuple) and len(result) == 2:
                diarization, emb_matrix = result
            else:
                # pyannote 4.x DiarizeOutput exposes `speaker_embeddings`
                emb_matrix = getattr(result, "speaker_embeddings", None)
                if emb_matrix is None:
                    emb_matrix = getattr(result, "embeddings", None)
            embeddings_map = build_embeddings_map(diarization, emb_matrix)
            if embeddings_map is None:
                print("Warning: embeddings requested but pipeline returned none")

        print(f"Diarization completed. Saving results to: {output_file}")

        if output_format == "rttm":
            # Save the diarization output to RTTM format (embeddings not
            # representable in RTTM; JSON format required for them)
            with open(output_file, "w") as rttm:
                diarization.write_rttm(rttm)
        else:
            # Save as JSON format
            save_json_format(diarization, output_file, audio_path,
                             embeddings_map=embeddings_map, model=model)

        # Print summary
        speakers = set()
        total_speech_time = 0.0

        # Iterate over speaker diarization
        # PyAnnote 4.x returns a DiarizeOutput object with a speaker_diarization attribute
        if hasattr(diarization, "speaker_diarization"):
            for turn, speaker in diarization.speaker_diarization:
                speakers.add(speaker)
                total_speech_time += turn.duration
        elif hasattr(diarization, "itertracks"):
            # Fallback for older versions
            for segment, track, speaker in diarization.itertracks(yield_label=True):
                speakers.add(speaker)
                total_speech_time += segment.duration
        else:
            # Try iterating directly (some versions return Annotation directly)
            for segment, track, speaker in diarization.itertracks(yield_label=True):
                speakers.add(speaker)
                total_speech_time += segment.duration

        print(f"\nDiarization Summary:")
        print(f"  Speakers detected: {len(speakers)}")
        print(f"  Speaker labels: {sorted(speakers)}")
        print(f"  Total speech time: {total_speech_time:.2f} seconds")
        print(f"  Output file saved: {output_file}")

    except Exception as e:
        print(f"Error during diarization: {e}")
        sys.exit(1)


def build_embeddings_map(diarization, emb_matrix):
    """Map speaker labels to their embedding vectors.

    pyannote orders embedding rows the same way as the annotation's labels().
    Handles both a raw Annotation and the 4.x output object.
    """
    if emb_matrix is None:
        return None
    ann = getattr(diarization, "speaker_diarization", diarization)
    if not hasattr(ann, "labels"):
        return None
    try:
        import numpy as np
        matrix = np.asarray(emb_matrix)
        labels = list(ann.labels())
        if matrix.ndim != 2 or matrix.shape[0] != len(labels):
            print(f"Warning: embedding matrix shape {matrix.shape} does not match "
                  f"{len(labels)} speakers — skipping embeddings")
            return None
        return {
            label: [float(x) for x in matrix[i]]
            for i, label in enumerate(labels)
        }
    except Exception as e:
        print(f"Warning: failed to build embeddings map: {e}")
        return None


def save_json_format(diarization, output_file: str, audio_path: str,
                     embeddings_map=None, model: str = "pyannote/speaker-diarization-community-1"):
    """Save diarization results in JSON format."""
    segments = []
    speakers = set()

    # PyAnnote 4.x
    if hasattr(diarization, "speaker_diarization"):
        for turn, speaker in diarization.speaker_diarization:
            segments.append({
                "start": turn.start,
                "end": turn.end,
                "speaker": speaker,
                "confidence": 1.0,
                "duration": turn.duration
            })
            speakers.add(speaker)
    # Older versions
    elif hasattr(diarization, "itertracks"):
        for segment, track, speaker in diarization.itertracks(yield_label=True):
            segments.append({
                "start": segment.start,
                "end": segment.end,
                "speaker": speaker,
                "confidence": 1.0,
                "duration": segment.duration
            })
            speakers.add(speaker)

    # Sort segments by start time
    segments.sort(key=lambda x: x["start"])

    results = {
        "audio_file": audio_path,
        "model": model,
        "segments": segments,
        "speakers": sorted(speakers),
        "speaker_count": len(speakers),
        "total_duration": max(seg["end"] for seg in segments) if segments else 0,
        "processing_info": {
            "total_segments": len(segments),
            "total_speech_time": sum(seg["duration"] for seg in segments)
        }
    }

    if embeddings_map:
        results["embeddings"] = embeddings_map

    with open(output_file, "w") as f:
        json.dump(results, f, indent=2)


def main():
    parser = argparse.ArgumentParser(
        description="Perform speaker diarization using PyAnnote.audio"
    )
    parser.add_argument(
        "audio_file",
        help="Path to audio file"
    )
    parser.add_argument(
        "--output", "-o",
        required=True,
        help="Output file path"
    )
    parser.add_argument(
        "--hf-token",
        required=True,
        help="Hugging Face access token"
    )
    parser.add_argument(
        "--model",
        default="pyannote/speaker-diarization-community-1",
        help="PyAnnote model to use"
    )
    parser.add_argument(
        "--min-speakers",
        type=int,
        help="Minimum number of speakers"
    )
    parser.add_argument(
        "--max-speakers",
        type=int,
        help="Maximum number of speakers"
    )
    parser.add_argument(
        "--output-format",
        choices=["rttm", "json"],
        default="rttm",
        help="Output format"
    )
    parser.add_argument(
        "--device",
        choices=["cpu", "cuda", "auto"],
        default="auto",
        help="Device to use for computation"
    )
    parser.add_argument(
        "--segmentation-onset",
        type=float,
        help="Voice activity detection onset threshold (0.0-1.0). Lower values detect quieter speech."
    )
    parser.add_argument(
        "--segmentation-offset",
        type=float,
        help="Voice activity detection offset/min_duration_off (0.0-1.0). Lower values are more sensitive to speech endings."
    )
    parser.add_argument(
        "--embeddings",
        action="store_true",
        help="Include per-speaker voice embeddings in JSON output (requires --output-format json)"
    )

    args = parser.parse_args()

    # Validate input file
    if not os.path.exists(args.audio_file):
        print(f"Error: Audio file not found: {args.audio_file}")
        sys.exit(1)

    # Validate speaker constraints
    if args.min_speakers is not None and args.min_speakers < 1:
        print("Error: min_speakers must be at least 1")
        sys.exit(1)

    if args.max_speakers is not None and args.max_speakers < 1:
        print("Error: max_speakers must be at least 1")
        sys.exit(1)

    if (args.min_speakers is not None and args.max_speakers is not None and
        args.min_speakers > args.max_speakers):
        print("Error: min_speakers cannot be greater than max_speakers")
        sys.exit(1)

    # Create output directory if it doesn't exist
    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)

    try:
        diarize_audio(
            audio_path=args.audio_file,
            output_file=args.output,
            hf_token=args.hf_token,
            model=args.model,
            min_speakers=args.min_speakers,
            max_speakers=args.max_speakers,
            output_format=args.output_format,
            device=args.device,
            segmentation_onset=args.segmentation_onset,
            segmentation_offset=args.segmentation_offset,
            include_embeddings=args.embeddings,
        )
    except Exception as e:
        print(f"Error during diarization: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
