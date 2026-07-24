package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"scriberr/internal/llm"
	"scriberr/internal/transcription/interfaces"
	"scriberr/pkg/logger"
)

// Domain-vocabulary support: ASR mis-hears domain terms (product/client/people
// names) into plausible-but-wrong words. Two interventions, both driven by one
// glossary (TRANSCRIPT_GLOSSARY env + the voice-library speaker names):
//   1. Hotwords — the glossary is fed to Whisper as initial_prompt to bias the
//      decoder toward those terms at the source.
//   2. LLM post-correction — after transcription, the transcript is corrected
//      segment-by-segment by the configured LLM, guided by the glossary.

const glossaryEnv = "TRANSCRIPT_GLOSSARY"

// buildGlossary combines the static env glossary with dynamic terms (voice
// library speaker names). Returns "" when nothing is configured.
func buildGlossary(dynamic []string) string {
	terms := []string{}
	if env := strings.TrimSpace(os.Getenv(glossaryEnv)); env != "" {
		terms = append(terms, env)
	}
	for _, d := range dynamic {
		if strings.TrimSpace(d) != "" {
			terms = append(terms, strings.TrimSpace(d))
		}
	}
	return strings.Join(terms, ", ")
}

// correctionEnabled gates the LLM post-correction pass (opt-in; needs an active
// LLM config to actually run). Hotwords are always applied when a glossary exists.
func correctionEnabled() bool {
	return strings.EqualFold(os.Getenv("TRANSCRIPT_CORRECTION"), "on")
}

// correctionSegment is the compact shape sent to / received from the LLM.
type correctionSegment struct {
	I    int    `json:"i"`
	Text string `json:"text"`
}

// correctionChunkSize bounds how many segments go into a single LLM request.
// The model has to echo every segment back, so a whole meeting (thousands of
// segments) overruns the reply token limit: the JSON comes back truncated and
// the strict validation below then discards the ENTIRE correction. That is why
// correction silently did nothing on real recordings — the logs showed
// "unparseable LLM response" / "duplicate-missing indices" on every long job.
// Chunking keeps each reply comfortably small and scopes a failure to its own
// chunk instead of the whole transcript.
const correctionChunkSize = 100

// correctTranscript rewrites segment TEXT to fix ASR errors, preserving every
// segment's timestamps and speaker. Fail-safe PER CHUNK: any parse/shape
// mismatch leaves that chunk's segments untouched — a wrong correction is worse
// than none — while the rest of the transcript is still corrected.
func correctTranscript(ctx context.Context, result *interfaces.TranscriptResult, glossary string, svc llm.Service, model string) {
	if svc == nil || result == nil || len(result.Segments) == 0 || strings.TrimSpace(glossary) == "" {
		return
	}

	system := "You correct automatic-speech-recognition errors in meeting transcripts. " +
		"Fix mis-heard words — especially the domain terms (product names, company names, " +
		"people's names) in this glossary: " + glossary + ". " +
		"Keep the SAME language as the input (do not translate). Preserve meaning; do not add, " +
		"remove, merge, split, reorder, or renumber segments. Return ONLY a JSON array of " +
		"{\"i\":<index>,\"text\":<corrected text>} with exactly the same indices you received."

	changed, failedChunks := 0, 0
	for start := 0; start < len(result.Segments); start += correctionChunkSize {
		end := start + correctionChunkSize
		if end > len(result.Segments) {
			end = len(result.Segments)
		}
		n, err := correctChunk(ctx, result, start, end, system, svc, model)
		if err != nil {
			failedChunks++
			logger.Warn("Correction: chunk kept original", "from", start, "to", end, "error", err)
			continue
		}
		changed += n
	}

	// Rebuild the flat text from the (possibly corrected) segments.
	var b strings.Builder
	for i := range result.Segments {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimSpace(result.Segments[i].Text))
	}
	result.Text = b.String()
	// word_segments are intentionally left as-is: correction is segment-level,
	// so the word-timing track may show pre-correction tokens (karaoke only);
	// segment text (what the transcript view shows) and all timestamps are exact.
	logger.Info("Transcript LLM-corrected", "segments_changed", changed,
		"total", len(result.Segments), "chunks_failed", failedChunks)
}

// correctChunk corrects result.Segments[start:end] in one LLM round-trip. It
// numbers segments locally (0..n-1) so the indices stay small and unambiguous
// regardless of where the chunk sits. Returns how many segments it changed; on
// any error nothing in the chunk is modified.
func correctChunk(ctx context.Context, result *interfaces.TranscriptResult, start, end int, system string, svc llm.Service, model string) (int, error) {
	in := make([]correctionSegment, 0, end-start)
	for i := start; i < end; i++ {
		in = append(in, correctionSegment{I: i - start, Text: result.Segments[i].Text})
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return 0, fmt.Errorf("marshal segments: %w", err)
	}

	resp, err := svc.ChatCompletion(ctx, model, []llm.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: string(payload)},
	}, 0.0)
	if err != nil {
		return 0, fmt.Errorf("llm call: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		return 0, errors.New("empty llm response")
	}

	var out []correctionSegment
	if err := json.Unmarshal([]byte(extractJSONArray(resp.Choices[0].Message.Content)), &out); err != nil {
		return 0, fmt.Errorf("unparseable response: %w", err)
	}
	if len(out) != len(in) {
		return 0, fmt.Errorf("segment count mismatch: got %d, want %d", len(out), len(in))
	}

	// Map corrected text back strictly by index; reject anything out of range.
	byIndex := make(map[int]string, len(out))
	for _, c := range out {
		if c.I < 0 || c.I >= len(in) {
			return 0, fmt.Errorf("out-of-range index %d", c.I)
		}
		byIndex[c.I] = c.Text
	}
	if len(byIndex) != len(in) {
		return 0, errors.New("duplicate or missing indices")
	}

	changed := 0
	for i := start; i < end; i++ {
		corrected := strings.TrimSpace(byIndex[i-start])
		if corrected != "" && corrected != strings.TrimSpace(result.Segments[i].Text) {
			result.Segments[i].Text = corrected
			changed++
		}
	}
	return changed, nil
}

// extractJSONArray pulls the first [...] block out of a model reply that may be
// wrapped in prose or ```json fences.
func extractJSONArray(s string) string {
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
