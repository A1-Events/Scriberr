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

// classifyJob labels one finished job. The return value reports whether an LLM
// request was made; classification is best-effort and must not fail the
// transcription.
func (u *UnifiedTranscriptionService) classifyJob(ctx context.Context, jobID string, transcript string, speakers []string) bool {
	if u.taggingRepo == nil || !classificationEnabled() || strings.TrimSpace(transcript) == "" {
		return false
	}
	// Never clobber a human's labels. The same job is re-classified whenever it
	// is re-transcribed (the start endpoint is reused for re-runs), so without
	// this a recording the user had corrected would silently revert to a machine
	// guess — losing both the curation and the example the classifier learns
	// from. A correction is final until the user changes it again.
	if manual, err := u.taggingRepo.IsManuallyLabelled(ctx, jobID); err != nil || manual {
		if manual {
			logger.Info("Classification skipped — labels were set by hand", "job_id", jobID)
		}
		return false
	}
	svc, model := u.llmServiceForCorrection(ctx)
	if svc == nil {
		logger.Info("Classification enabled but no usable LLM config/model — skipping")
		return false
	}

	companies, err := u.taggingRepo.ListCompanies(ctx)
	if err != nil || len(companies) == 0 {
		logger.Warn("Classification: no taxonomy configured — skipping", "error", err)
		return false
	}
	tags, err := u.taggingRepo.ListTags(ctx)
	if err != nil {
		logger.Warn("Classification: could not load tags — skipping", "error", err)
		return false
	}

	system := buildClassifyPrompt(companies, tags)
	if examples, err := u.taggingRepo.CorrectedExamples(ctx, classifyExamples); err == nil && len(examples) > 0 {
		system += "\n\n" + renderExamples(examples)
	}

	speakers = u.namedSpeakersForJob(ctx, jobID, speakers)
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
		return true
	}

	var out classifyResult
	if err := json.Unmarshal([]byte(extractJSONObject(resp.Choices[0].Message.Content)), &out); err != nil {
		logger.Warn("Classification: unparseable response — leaving unlabelled", "error", err)
		return true
	}

	companyID, ok := companyIDForKey(companies, out.Company)
	if !ok {
		// The model may only choose from the curated taxonomy; anything else is
		// treated as "don't know" so the taxonomy cannot drift on its own.
		logger.Warn("Classification: unknown company key — leaving unlabelled", "key", out.Company)
		return true
	}
	tagIDs := tagIDsForKeys(tags, out.Tags, companyID)

	conf := out.Confidence
	if err := u.taggingRepo.SetLabels(ctx, jobID, &companyID, tagIDs, models.SourceAuto, &conf); err != nil {
		logger.Warn("Classification: could not save labels", "error", err)
		return true
	}
	logger.Info("Recording classified", "job_id", jobID, "company", out.Company,
		"tags", strings.Join(out.Tags, ","), "confidence", conf)
	return true
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
		text, _ := flattenStored(ex.Transcript)
		fmt.Fprintf(&b, "- [%s / %s] %s\n", ex.CompanyKey, strings.Join(ex.TagKeys, "+"),
			strings.ReplaceAll(excerpt(text, classifyExampleChars), "\n", " "))
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
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
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

// storedTranscript is the shape saved in TranscriptionJob.Transcript: WhisperX
// JSON with a segments array. Older rows may be a bare array.
type storedTranscript struct {
	// Some adapters (Parakeet/Canary with timestamps disabled) save the whole
	// transcript in Text and leave Segments empty, so Text is the fallback —
	// without it those recordings look empty and can never be backfilled.
	Text     string `json:"text"`
	Segments []struct {
		Text    string  `json:"text"`
		Speaker *string `json:"speaker"`
	} `json:"segments"`
}

// flattenStored pulls plain text and the distinct speaker labels out of a
// stored transcript, so an already-finished recording can be classified without
// re-running transcription.
func flattenStored(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if raw[0] != '[' && raw[0] != '{' {
		return raw, nil // plain text row
	}
	var st storedTranscript
	segs := st.Segments
	if raw[0] == '{' {
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			return "", nil
		}
		segs = st.Segments
	} else {
		if err := json.Unmarshal([]byte(raw), &segs); err != nil {
			return "", nil
		}
	}
	var b strings.Builder
	seen := map[string]bool{}
	var speakers []string
	for i, s := range segs {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimSpace(s.Text))
		if s.Speaker != nil && *s.Speaker != "" && !seen[*s.Speaker] {
			seen[*s.Speaker] = true
			speakers = append(speakers, *s.Speaker)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		// No usable segments — fall back to the top-level text.
		out = strings.TrimSpace(st.Text)
	}
	return out, speakers
}

// ClassifyExisting labels finished recordings that have no company yet, using
// their stored transcript. Needed because the classifier normally runs only at
// the end of a transcription — without this, labelling a pre-existing library
// would mean re-transcribing everything.
//
// Writes Source=auto, exactly like the inline pass: these are machine guesses,
// and must NOT be mistaken for the human corrections the classifier learns from.
func (u *UnifiedTranscriptionService) ClassifyExisting(ctx context.Context, limit int) (int, int, error) {
	if u.taggingRepo == nil {
		return 0, 0, fmt.Errorf("tagging is not enabled")
	}
	if !classificationEnabled() {
		return 0, 0, fmt.Errorf("automatic classification is disabled (set AUTO_CLASSIFY=on)")
	}
	jobs, err := u.jobRepo.FindByStatus(ctx, models.StatusCompleted)
	if err != nil {
		return 0, 0, err
	}
	classified, skipped, attempted := 0, 0, 0
	for _, job := range jobs {
		// Bound on ATTEMPTS, not successes: a job whose classification fails
		// still costs an LLM call, so counting only successes would let
		// limit=1 quietly send the entire library to the model.
		if limit > 0 && attempted >= limit {
			break
		}
		if job.CompanyID != nil || job.Transcript == nil {
			skipped++
			continue
		}
		text, speakers := flattenStored(*job.Transcript)
		if strings.TrimSpace(text) == "" {
			skipped++
			continue
		}
		if !u.classifyJob(ctx, job.ID, text, speakers) {
			skipped++
			continue
		}
		attempted++
		// classifyJob is fail-safe and stays silent on error, so re-read to see
		// whether it took. Jobs that already had a company were skipped above,
		// so a company here means this pass set it.
		if cid, _, _, err := u.taggingRepo.LabelsForJob(ctx, job.ID); err == nil && cid != nil {
			classified++
		} else {
			skipped++
		}
	}
	logger.Info("Backfill classification finished", "classified", classified, "skipped", skipped)
	return classified, skipped, nil
}
