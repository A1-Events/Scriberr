package transcription

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"scriberr/internal/llm"
	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/pkg/logger"
)

// Auto-classification of a finished recording into one company plus any number
// of project tags.
//
// The interesting part is the feedback loop: every label a human sets through
// the API is stored with Source=manual, and the most recent of those are fed
// back into this prompt as worked examples. So each correction immediately
// steers subsequent classifications, with no training step and nothing to keep
// in sync — "what has it learned" is just a query over the manual rows.
//
// Fail-safe, like the correction pass: on any error the recording is left
// UNLABELLED rather than mislabelled. A wrong bucket is worse than no bucket,
// because a wrong one hides the recording from the place you would look.

// classificationEnabled gates the pass. Off unless explicitly enabled: it sends
// a transcript excerpt to the configured LLM, which must be a deliberate choice.
func classificationEnabled() bool {
	return strings.EqualFold(os.Getenv("AUTO_CLASSIFY"), "on")
}

const (
	// Enough to judge the subject without paying for a whole meeting.
	classifyExcerptChars = 3000
	// How many past corrections to show as worked examples.
	classifyExamples = 12
	// Excerpt per example — short, since only the gist is needed.
	classifyExampleChars = 400
)

type classifyResult struct {
	Company    string   `json:"company"`
	Tags       []string `json:"tags"`
	Confidence float64  `json:"confidence"`
}

// classifyJob labels one finished job. Never returns an error: classification
// is best-effort and must not fail the transcription.
func (u *UnifiedTranscriptionService) classifyJob(ctx context.Context, jobID string, transcript string, speakers []string) {
	if u.taggingRepo == nil || !classificationEnabled() || strings.TrimSpace(transcript) == "" {
		return
	}
	svc, model := u.llmServiceForCorrection(ctx)
	if svc == nil {
		logger.Info("Classification enabled but no usable LLM config/model — skipping")
		return
	}

	companies, err := u.taggingRepo.ListCompanies(ctx)
	if err != nil || len(companies) == 0 {
		logger.Warn("Classification: no taxonomy configured — skipping", "error", err)
		return
	}
	tags, err := u.taggingRepo.ListTags(ctx)
	if err != nil {
		logger.Warn("Classification: could not load tags — skipping", "error", err)
		return
	}

	system := buildClassifyPrompt(companies, tags)
	if examples, err := u.taggingRepo.CorrectedExamples(ctx, classifyExamples); err == nil && len(examples) > 0 {
		system += "\n\n" + renderExamples(examples)
	}

	user := excerpt(transcript, classifyExcerptChars)
	if len(speakers) > 0 {
		// Who is in the room predicts the company well — often better than the
		// words, since the same topics recur across businesses.
		user = "Speakers present: " + strings.Join(speakers, ", ") + "\n\n" + user
	}

	resp, err := svc.ChatCompletion(ctx, model, []llm.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, 0.0)
	if err != nil || resp == nil || len(resp.Choices) == 0 {
		logger.Warn("Classification: LLM call failed — leaving unlabelled", "error", err)
		return
	}

	var out classifyResult
	if err := json.Unmarshal([]byte(extractJSONObject(resp.Choices[0].Message.Content)), &out); err != nil {
		logger.Warn("Classification: unparseable response — leaving unlabelled", "error", err)
		return
	}

	companyID, ok := companyIDForKey(companies, out.Company)
	if !ok {
		// The model may only choose from the curated taxonomy; anything else is
		// treated as "don't know" so the taxonomy cannot drift on its own.
		logger.Warn("Classification: unknown company key — leaving unlabelled", "key", out.Company)
		return
	}
	tagIDs := tagIDsForKeys(tags, out.Tags, companyID)

	conf := out.Confidence
	if err := u.taggingRepo.SetLabels(ctx, jobID, &companyID, tagIDs, models.SourceAuto, &conf); err != nil {
		logger.Warn("Classification: could not save labels", "error", err)
		return
	}
	logger.Info("Recording classified", "job_id", jobID, "company", out.Company,
		"tags", strings.Join(out.Tags, ","), "confidence", conf)
}

func buildClassifyPrompt(companies []models.Company, tags []models.Tag) string {
	var b strings.Builder
	b.WriteString("You file meeting recordings into a fixed taxonomy.\n\n")
	b.WriteString("COMPANIES (choose exactly ONE, by key):\n")
	for _, c := range companies {
		fmt.Fprintf(&b, "  %s — %s\n", c.Key, c.Name)
	}
	b.WriteString("\nPROJECT TAGS (choose ZERO OR MORE, by key; a meeting may span several):\n")
	byCompany := map[uint][]models.Tag{}
	var global []models.Tag
	for _, t := range tags {
		if t.CompanyID == nil {
			global = append(global, t)
		} else {
			byCompany[*t.CompanyID] = append(byCompany[*t.CompanyID], t)
		}
	}
	for _, c := range companies {
		for _, t := range byCompany[c.ID] {
			fmt.Fprintf(&b, "  %s (%s) — %s\n", t.Key, c.Key, t.Name)
		}
	}
	for _, t := range global {
		fmt.Fprintf(&b, "  %s (any) — %s\n", t.Key, t.Name)
	}
	b.WriteString("\nRules:\n")
	b.WriteString("- Use ONLY keys listed above. Never invent a key.\n")
	b.WriteString("- Prefer tags belonging to the company you chose.\n")
	b.WriteString("- If genuinely unsure of the company, still pick the closest and lower the confidence.\n")
	b.WriteString("- confidence is 0..1 for the company choice.\n")
	b.WriteString("\nReturn ONLY JSON: {\"company\":\"KEY\",\"tags\":[\"KEY\"],\"confidence\":0.0}")
	return b.String()
}

// renderExamples turns past human corrections into worked examples. This is the
// entire learning mechanism.
func renderExamples(examples []repository.CorrectedExample) string {
	var b strings.Builder
	b.WriteString("WORKED EXAMPLES — these were corrected by the user, so follow their pattern:\n")
	for _, ex := range examples {
		if ex.CompanyKey == "" {
			continue
		}
		fmt.Fprintf(&b, "- [%s / %s] %s\n", ex.CompanyKey, strings.Join(ex.TagKeys, "+"),
			strings.ReplaceAll(excerpt(ex.Transcript, classifyExampleChars), "\n", " "))
	}
	return b.String()
}

func companyIDForKey(companies []models.Company, key string) (uint, bool) {
	for _, c := range companies {
		if strings.EqualFold(c.Key, strings.TrimSpace(key)) {
			return c.ID, true
		}
	}
	return 0, false
}

// tagIDsForKeys resolves keys to ids, keeping only tags that are global or
// belong to the chosen company, and de-duplicating.
func tagIDsForKeys(tags []models.Tag, keys []string, companyID uint) []uint {
	seen := map[uint]bool{}
	var out []uint
	for _, k := range keys {
		for _, t := range tags {
			if !strings.EqualFold(t.Key, strings.TrimSpace(k)) {
				continue
			}
			if t.CompanyID != nil && *t.CompanyID != companyID {
				continue
			}
			if !seen[t.ID] {
				seen[t.ID] = true
				out = append(out, t.ID)
			}
		}
	}
	return out
}

func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// extractJSONObject pulls the first {...} block out of a reply that may be
// wrapped in prose or ```json fences.
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
