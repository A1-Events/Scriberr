package transcription

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"scriberr/internal/repository"
)

func TestExcerptDoesNotSplitUnicode(t *testing.T) {
	got := excerpt("ședință despre piață", 7)
	require.True(t, utf8.ValidString(got))
	require.Equal(t, "ședință", got)
}

func TestRenderExamplesFlattensStoredTranscript(t *testing.T) {
	raw := `{"text":"fallback","segments":[{"text":"Discussed the launch","speaker":"Alice"},{"text":"and the budget","speaker":"Bob"}]}`
	got := renderExamples([]repository.CorrectedExample{{
		CompanyKey: "A1-EVENTS",
		TagKeys:    []string{"CHRISTMAS-MARKET"},
		Transcript: raw,
	}})

	require.Contains(t, got, "Discussed the launch and the budget")
	require.False(t, strings.Contains(got, "segments"))
}
