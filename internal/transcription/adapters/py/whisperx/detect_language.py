#!/usr/bin/env python3
"""Pick the spoken language by sampling several windows of the audio.

WhisperX's own detection (whisperx/asr.py, FasterWhisperPipeline.detect_language)
reads ONLY the first 30 seconds and discards the probability it gets back:

    segment = log_mel_spectrogram(audio[: N_SAMPLES], ...)
    language_token, language_probability = results[0][0]
    return language                      # probability is logged, never checked

So an unrepresentative opening decides the whole file, and a 0.19 guess is
honoured exactly like a 0.96 one. That is not a cosmetic bug: when Whisper is
given the wrong language it TRANSLATES instead of transcribing, so the output
looks clean and fluent while being entirely the wrong language. Measured on a
55-minute Romanian meeting that opens with a short greeting:

    t=   0s -> en (p=0.19)   <- the only window WhisperX looks at
    t=  60s -> ro (p=0.82)
    t= 300s -> ro (p=0.96)
    t= 900s -> ro (p=0.92)
    t=1800s -> ro (p=0.66)

Sample across the recording, ignore near-silent windows, and sum probability per
language so one unlucky window cannot outvote the body of the audio.

Prints "LANG=<code>" on success. Any failure exits non-zero WITHOUT printing, so
the caller falls back to WhisperX's built-in detection rather than guessing.
"""

import argparse
import sys

# Fractions of the recording to sample. 0.0 is included so short files still
# work; the rest spread through the body, which is where representative speech
# is. Order does not matter — every window is weighted by its own confidence.
SAMPLE_POINTS = (0.0, 0.1, 0.25, 0.5, 0.75, 0.9)
WINDOW_SECONDS = 30
SAMPLE_RATE = 16000
# Below this RMS a window is effectively silence; language ID on silence returns
# a confident-looking answer drawn from noise, so drop it rather than let it vote.
SILENCE_RMS = 0.005


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("audio")
    ap.add_argument("--model", required=True)
    ap.add_argument("--device", default="cuda")
    ap.add_argument("--compute-type", default="float16")
    ap.add_argument("--device-index", type=int, default=0)
    ap.add_argument("--model-dir", default=None)
    args = ap.parse_args()

    import numpy as np
    from faster_whisper import WhisperModel
    from faster_whisper.audio import decode_audio

    audio = decode_audio(args.audio, sampling_rate=SAMPLE_RATE)
    total = audio.shape[0]
    if total == 0:
        print("empty audio", file=sys.stderr)
        return 1

    window = WINDOW_SECONDS * SAMPLE_RATE
    starts = []
    for frac in SAMPLE_POINTS:
        start = int(total * frac)
        # Keep the window inside the audio; for files shorter than one window
        # this collapses to a single start at 0 and is deduped below.
        start = max(0, min(start, total - window))
        if start not in starts:
            starts.append(start)

    model = WhisperModel(
        args.model,
        device=args.device,
        device_index=args.device_index,
        compute_type=args.compute_type,
        download_root=args.model_dir,
    )

    scores: dict[str, float] = {}
    voted = 0
    for start in starts:
        chunk = audio[start:start + window]
        if chunk.shape[0] < SAMPLE_RATE:  # < 1s of audio left; nothing to judge
            continue
        if float(np.sqrt((chunk.astype(np.float32) ** 2).mean())) < SILENCE_RMS:
            continue
        lang, prob, _ = model.detect_language(chunk)
        scores[lang] = scores.get(lang, 0.0) + prob
        voted += 1
        print(f"  window {start // SAMPLE_RATE:>5}s -> {lang} ({prob:.2f})", file=sys.stderr)

    if not scores:
        # Every window was silence or too short — let WhisperX decide.
        print("no usable windows", file=sys.stderr)
        return 1

    best = max(scores, key=lambda k: scores[k])
    print(f"language={best} from {voted} window(s); scores={scores}", file=sys.stderr)
    print(f"LANG={best}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
