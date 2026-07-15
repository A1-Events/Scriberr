package transcription

import (
	"context"
	"errors"
	"testing"

	"scriberr/internal/llm"
	"scriberr/internal/transcription/interfaces"
)

// stubLLM returns a canned content string (or an error) from ChatCompletion.
type stubLLM struct {
	content string
	err     error
}

func (s *stubLLM) ChatCompletion(_ context.Context, _ string, _ []llm.ChatMessage, _ float64) (*llm.ChatResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	r := &llm.ChatResponse{}
	r.Choices = append(r.Choices, struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	}{})
	r.Choices[0].Message.Content = s.content
	return r, nil
}

func (s *stubLLM) ChatCompletionStream(_ context.Context, _ string, _ []llm.ChatMessage, _ float64) (<-chan string, <-chan error) {
	return nil, nil
}

func (s *stubLLM) GetContextWindow(_ context.Context, _ string) (int, error) { return 128000, nil }
func (s *stubLLM) GetModels(_ context.Context) ([]string, error)             { return nil, nil }

func mkResult() *interfaces.TranscriptResult {
	return &interfaces.TranscriptResult{
		Segments: []interfaces.TranscriptSegment{
			{Start: 0, End: 2, Text: "ne oprim vertical"},
			{Start: 2, End: 4, Text: "clientul prosper"},
		},
	}
}

func TestCorrectionApplies(t *testing.T) {
	res := mkResult()
	svc := &stubLLM{content: `[{"i":0,"text":"ne oprim Vertica"},{"i":1,"text":"clientul Prospero"}]`}
	correctTranscript(context.Background(), res, "Vertica, Prospero", svc, "m")
	if res.Segments[0].Text != "ne oprim Vertica" || res.Segments[1].Text != "clientul Prospero" {
		t.Fatalf("correction not applied: %+v", res.Segments)
	}
	if res.Text != "ne oprim Vertica clientul Prospero" {
		t.Fatalf("flat text not rebuilt: %q", res.Text)
	}
}

func TestCorrectionFailSafe(t *testing.T) {
	orig0, orig1 := "ne oprim vertical", "clientul prosper"
	cases := map[string]*stubLLM{
		"llm error":        {err: errors.New("boom")},
		"unparseable":      {content: "sorry, I can't do that"},
		"count mismatch":   {content: `[{"i":0,"text":"only one"}]`},
		"out of range idx": {content: `[{"i":0,"text":"a"},{"i":5,"text":"b"}]`},
		"garbage prose":    {content: "here you go: [not json]"},
	}
	for name, svc := range cases {
		res := mkResult()
		correctTranscript(context.Background(), res, "Vertica", svc, "m")
		if res.Segments[0].Text != orig0 || res.Segments[1].Text != orig1 {
			t.Fatalf("[%s] fail-safe violated — transcript mutated: %+v", name, res.Segments)
		}
	}
}

func TestGlossaryBuild(t *testing.T) {
	t.Setenv("TRANSCRIPT_GLOSSARY", "Vertica, Prospero")
	g := buildGlossary([]string{"Andrei DR", "Cristi Mihoc", ""})
	if g != "Vertica, Prospero, Andrei DR, Cristi Mihoc" {
		t.Fatalf("unexpected glossary: %q", g)
	}
}
