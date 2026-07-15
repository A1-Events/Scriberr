package transcription

import (
	"context"
	"encoding/json"
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

// correctTranscript rewrites segment TEXT to fix ASR errors, preserving every
// segment's timestamps and speaker. Fail-safe: any parse/shape mismatch leaves
// the transcript untouched — a wrong correction is worse than none.
func correctTranscript(ctx context.Context, result *interfaces.TranscriptResult, glossary string, svc llm.Service, model string) {
	if svc == nil || result == nil || len(result.Segments) == 0 || strings.TrimSpace(glossary) == "" {
		return
	}

	in := make([]correctionSegment, len(result.Segments))
	for i, seg := range result.Segments {
		in[i] = correctionSegment{I: i, Text: seg.Text}
	}
	payload, err := json.Marshal(in)
	if err != nil {
		logger.Warn("Correction: failed to marshal segments", "error", err)
		return
	}

	system := "You correct automatic-speech-recognition errors in meeting transcripts. " +
		"Fix mis-heard words — especially the domain terms (product names, company names, " +
		"people's names) in this glossary: " + glossary + ". " +
		"Keep the SAME language as the input (do not translate). Preserve meaning; do not add, " +
		"remove, merge, split, reorder, or renumber segments. Return ONLY a JSON array of " +
		"{\"i\":<index>,\"text\":<corrected text>} with exactly the same indices you received."
	messages := []llm.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: string(payload)},
	}

	resp, err := svc.ChatCompletion(ctx, model, messages, 0.0)
	if err != nil || resp == nil || len(resp.Choices) == 0 {
		logger.Warn("Correction: LLM call failed — keeping original transcript", "error", err)
		return
	}
	content := resp.Choices[0].Message.Content

	var out []correctionSegment
	if err := json.Unmarshal([]byte(extractJSONArray(content)), &out); err != nil {
		logger.Warn("Correction: unparseable LLM response — keeping original transcript", "error", err)
		return
	}
	if len(out) != len(result.Segments) {
		logger.Warn("Correction: segment count mismatch — keeping original transcript",
			"got", len(out), "want", len(result.Segments))
		return
	}

	// Map corrected text back strictly by index; reject out-of-range indices.
	byIndex := make(map[int]string, len(out))
	for _, c := range out {
		if c.I < 0 || c.I >= len(result.Segments) {
			logger.Warn("Correction: out-of-range index — keeping original transcript", "index", c.I)
			return
		}
		byIndex[c.I] = c.Text
	}
	if len(byIndex) != len(result.Segments) {
		logger.Warn("Correction: duplicate/missing indices — keeping original transcript")
		return
	}

	changed := 0
	var b strings.Builder
	for i := range result.Segments {
		corrected := strings.TrimSpace(byIndex[i])
		if corrected != "" && corrected != strings.TrimSpace(result.Segments[i].Text) {
			result.Segments[i].Text = corrected
			changed++
		}
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimSpace(result.Segments[i].Text))
	}
	result.Text = b.String()
	// word_segments are intentionally left as-is: correction is segment-level,
	// so the word-timing track may show pre-correction tokens (karaoke only);
	// segment text (what the transcript view shows) and all timestamps are exact.
	logger.Info("Transcript LLM-corrected", "segments_changed", changed, "total", len(result.Segments))
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
