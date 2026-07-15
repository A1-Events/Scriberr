package transcription

import (
	"testing"

	"scriberr/internal/transcription/interfaces"
)

func seg(start, end float64, text string) interfaces.TranscriptSegment {
	return interfaces.TranscriptSegment{Start: start, End: end, Text: text}
}

func TestFilterHallucinations(t *testing.T) {
	cfg := HallucinationConfig{Enabled: true, MinRepeatRun: 6}
	res := &interfaces.TranscriptResult{
		Segments: []interfaces.TranscriptSegment{
			seg(0, 2, "Laptopul lui Andrei nu vrea să intre."), // legit RO — keep
			seg(2, 4, "Like and subscribe!"),                   // hallucination — drop
			seg(4, 6, "Să apăsați pe graficul de velocity."),  // legit (contains no phrase) — keep
			seg(6, 8, "da da da da da da da da"),               // repetition loop — drop
			seg(8, 10, "Mulțumesc."),                          // real thanks — MUST keep (precision)
			seg(10, 12, "Mulțumesc pentru vizionare"),         // RO caption artifact — drop
		},
		WordSegments: []interfaces.TranscriptWord{
			{Start: 0.5, End: 1, Word: "Laptopul"}, // in kept segment
			{Start: 2.5, End: 3, Word: "subscribe"}, // in dropped segment
			{Start: 4.5, End: 5, Word: "velocity"}, // in kept segment
		},
	}

	removed := filterHallucinations(res, cfg)
	if removed != 3 {
		t.Fatalf("expected 3 removed, got %d", removed)
	}
	if len(res.Segments) != 3 {
		t.Fatalf("expected 3 segments kept, got %d", len(res.Segments))
	}
	if res.Segments[0].Text != "Laptopul lui Andrei nu vrea să intre." ||
		res.Segments[1].Text != "Să apăsați pe graficul de velocity." ||
		res.Segments[2].Text != "Mulțumesc." {
		t.Fatalf("wrong segments kept: %+v", res.Segments)
	}
	// Word inside the dropped "Like and subscribe" segment must be gone.
	if len(res.WordSegments) != 2 {
		t.Fatalf("expected 2 words kept, got %d: %+v", len(res.WordSegments), res.WordSegments)
	}
	for _, w := range res.WordSegments {
		if w.Word == "subscribe" {
			t.Fatalf("word from dropped segment survived")
		}
	}
}

func TestFilterDisabled(t *testing.T) {
	res := &interfaces.TranscriptResult{Segments: []interfaces.TranscriptSegment{seg(0, 2, "Thank you.")}}
	if n := filterHallucinations(res, HallucinationConfig{Enabled: false}); n != 0 {
		t.Fatalf("disabled filter removed %d", n)
	}
	if len(res.Segments) != 1 {
		t.Fatalf("disabled filter mutated segments")
	}
}

func TestRepetitionLoopGuard(t *testing.T) {
	// A real word repeated (not a short degenerate token) must NOT be caught.
	if isRepetitionLoop("really really really really really really", 6) {
		t.Fatalf("real-word repetition wrongly flagged as loop")
	}
	// Short token loop IS caught.
	if !isRepetitionLoop("da da da da da da", 6) {
		t.Fatalf("short-token loop not caught")
	}
	// Below the run threshold is not caught.
	if isRepetitionLoop("da da da", 6) {
		t.Fatalf("short run wrongly flagged")
	}
}
