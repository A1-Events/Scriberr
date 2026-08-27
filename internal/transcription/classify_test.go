package transcription

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"scriberr/internal/repository"
)

func TestClassifyExistingRejectsMissingLLMConfiguration(t *testing.T) {
	t.Setenv("AUTO_CLASSIFY", "on")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	service := &UnifiedTranscriptionService{taggingRepo: repository.NewTaggingRepository(db)}

	_, _, err = service.ClassifyExisting(context.Background(), 10)
	require.ErrorContains(t, err, "active LLM provider")
}

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
