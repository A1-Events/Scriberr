package transcription

import (
	"os"
	"regexp"
	"strings"

	"scriberr/internal/transcription/interfaces"
	"scriberr/pkg/logger"
)

// Whisper (and Whisper-derived models) hallucinate stock phrases on silence,
// breathing, or background noise — artifacts of YouTube-heavy training data.
// This filter drops segments that are ONLY such a phrase, plus degenerate
// repetition loops (e.g. "da da da da…", which also catches VibeVoice-style
// repetition). It works on segment text alone, so it applies to every adapter.
//
// Matching is whole-segment (the normalized segment text must BE the phrase),
// so legitimate speech that merely contains a word like "subscribe" is never
// dropped. Toggle off with HALLUCINATION_FILTER=off.

var hallucinationNormalizer = regexp.MustCompile(`[^\p{L}\p{N}\s]+`)

// Known stock hallucination phrases, normalized (lowercased, punctuation
// stripped, whitespace collapsed). Multilingual: these surface regardless of
// spoken language because they come from the model's caption training data.
var hallucinationPhrases = normalizeSet([]string{
	// English (YouTube caption artifacts)
	"like and subscribe",
	"please like and subscribe",
	"thanks for watching",
	"thank you for watching",
	"dont forget to subscribe",
	"dont forget to like and subscribe",
	"subscribe to my channel",
	"see you in the next video",
	"see you next time",
	"subtitles by the amaraorg community",
	"subtitles by amaraorg",
	"transcription by castingwords",
	// NOTE: bare "thank you" / "thanks" are deliberately NOT listed — they are
	// common real speech. Only the unambiguous caption artifacts above/below
	// (which nobody says in a meeting) are filtered. Under-filter > delete
	// real words.
	// Romanian
	"abonativa",
	"aboneazate",
	"multumesc pentru vizionare",
	"va multumesc pentru vizionare",
	"ne vedem in urmatorul videoclip",
	"subtitrarea a fost realizata de comunitatea amaraorg",
	// Other languages that leak in on mixed audio
	"untertitel der amaraorg community",
	"sous titres realises par la communaute damaraorg",
	"amaraorg",
})

// HallucinationConfig controls the filter. Defaults are conservative.
type HallucinationConfig struct {
	Enabled bool
	// MinRepeatRun: a segment whose text is one short token repeated at least
	// this many times consecutively is treated as a repetition-loop hallucination.
	MinRepeatRun int
}

func hallucinationConfigFromEnv() HallucinationConfig {
	cfg := HallucinationConfig{Enabled: true, MinRepeatRun: 6}
	if strings.EqualFold(os.Getenv("HALLUCINATION_FILTER"), "off") {
		cfg.Enabled = false
	}
	return cfg
}

// diacriticFolder maps accented Latin letters to their base form so matching
// is robust to the model emitting "mulțumesc" vs "multumesc", "café" vs "cafe",
// etc. (Whisper is inconsistent about diacritics.)
var diacriticFolder = strings.NewReplacer(
	"ă", "a", "â", "a", "à", "a", "á", "a", "ä", "a", "ã", "a", "å", "a",
	"î", "i", "ì", "i", "í", "i", "ï", "i",
	"ș", "s", "ş", "s",
	"ț", "t", "ţ", "t",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n", "ß", "ss",
)

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = diacriticFolder.Replace(s)
	s = hallucinationNormalizer.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func normalizeSet(in []string) map[string]struct{} {
	m := make(map[string]struct{}, len(in))
	for _, s := range in {
		m[normalize(s)] = struct{}{}
	}
	return m
}

// isRepetitionLoop reports whether the normalized text is a single short token
// repeated minRun+ times (e.g. "da da da da da da"), a classic degenerate
// decode. Requires the token to be short (<=4 chars) so genuine emphatic
// repetition of real words isn't over-caught.
func isRepetitionLoop(norm string, minRun int) bool {
	fields := strings.Fields(norm)
	if len(fields) < minRun {
		return false
	}
	first := fields[0]
	if len(first) == 0 || len(first) > 4 {
		return false
	}
	for _, f := range fields {
		if f != first {
			return false
		}
	}
	return true
}

// filterHallucinations removes hallucinated segments from the result in place
// and drops any word_segments falling inside a removed segment's time span.
// Returns the number of segments removed.
func filterHallucinations(result *interfaces.TranscriptResult, cfg HallucinationConfig) int {
	if !cfg.Enabled || result == nil || len(result.Segments) == 0 {
		return 0
	}

	kept := result.Segments[:0:0]
	var removedSpans [][2]float64
	removed := 0
	for _, seg := range result.Segments {
		norm := normalize(seg.Text)
		_, isPhrase := hallucinationPhrases[norm]
		if norm != "" && (isPhrase || isRepetitionLoop(norm, cfg.MinRepeatRun)) {
			removedSpans = append(removedSpans, [2]float64{seg.Start, seg.End})
			removed++
			continue
		}
		kept = append(kept, seg)
	}
	if removed == 0 {
		return 0
	}
	result.Segments = kept

	// Drop words that fall within a removed segment's span so the word-level
	// (karaoke) track stays consistent with the segment track.
	if len(result.WordSegments) > 0 {
		keptWords := result.WordSegments[:0:0]
		for _, w := range result.WordSegments {
			mid := (w.Start + w.End) / 2
			drop := false
			for _, span := range removedSpans {
				if mid >= span[0] && mid <= span[1] {
					drop = true
					break
				}
			}
			if !drop {
				keptWords = append(keptWords, w)
			}
		}
		result.WordSegments = keptWords
	}

	// Rebuild the flat text from surviving segments.
	var b strings.Builder
	for i, seg := range result.Segments {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimSpace(seg.Text))
	}
	result.Text = b.String()

	logger.Info("Filtered hallucinated segments", "removed", removed, "remaining", len(result.Segments))
	return removed
}
