package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"

	"gorm.io/gorm"

	"scriberr/internal/models"
	"scriberr/pkg/logger"
)

// DefaultVoiceMatchThreshold is the cosine-similarity floor for auto-matching
// a recording's speaker against the library. Override via VOICE_MATCH_THRESHOLD.
const DefaultVoiceMatchThreshold = 0.72

// VoiceLibraryRepository persists global speakers and per-recording voiceprints
// and implements the matching + learning logic of the voice library.
type VoiceLibraryRepository interface {
	// StoreVoiceprints saves the embeddings extracted for one job and matches
	// each against the library. Returns original_speaker -> matched Speaker
	// (only entries at/above the threshold).
	StoreVoiceprints(ctx context.Context, jobID string, embeddings map[string][]float32) (map[string]models.Speaker, error)
	// LearnFromJob folds the voiceprint of (jobID, originalSpeaker) into the
	// named speaker's centroid, creating the speaker if needed.
	LearnFromJob(ctx context.Context, jobID, originalSpeaker, name string) error
	// SuggestionsForJob returns original_speaker -> (speaker, confidence) for
	// every matched voiceprint of the job.
	SuggestionsForJob(ctx context.Context, jobID string) (map[string]MatchSuggestion, error)
	ListSpeakers(ctx context.Context) ([]models.Speaker, error)
	RenameSpeaker(ctx context.Context, id uint, name string) error
	DeleteSpeaker(ctx context.Context, id uint) error
	// MergeSpeakers folds `from` into `to` (weighted centroid, voiceprint
	// re-pointing) and deletes `from`.
	MergeSpeakers(ctx context.Context, fromID, toID uint) error
	// ApplyMatchesAsMappings creates SpeakerMapping rows for library matches,
	// never overwriting an existing (human-made) mapping for the same label.
	ApplyMatchesAsMappings(ctx context.Context, jobID string, matches map[string]models.Speaker) error
}

// MatchSuggestion is a library match for one diarized speaker label.
type MatchSuggestion struct {
	SpeakerID  uint    `json:"speaker_id"`
	Name       string  `json:"name"`
	Confidence float64 `json:"confidence"`
}

type voiceLibraryRepository struct {
	db        *gorm.DB
	threshold float64
}

func NewVoiceLibraryRepository(db *gorm.DB) VoiceLibraryRepository {
	threshold := DefaultVoiceMatchThreshold
	if env := os.Getenv("VOICE_MATCH_THRESHOLD"); env != "" {
		if v, err := strconv.ParseFloat(env, 64); err == nil && v > 0 && v < 1 {
			threshold = v
		} else {
			logger.Warn("Invalid VOICE_MATCH_THRESHOLD, using default", "value", env)
		}
	}
	return &voiceLibraryRepository{db: db, threshold: threshold}
}

func encodeEmbedding(v []float32) ([]byte, error)  { return json.Marshal(v) }
func decodeEmbedding(b []byte) ([]float32, error) {
	var v []float32
	err := json.Unmarshal(b, &v)
	return v, err
}

func l2Normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return v
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return -1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return -1
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func (r *voiceLibraryRepository) StoreVoiceprints(ctx context.Context, jobID string, embeddings map[string][]float32) (map[string]models.Speaker, error) {
	speakers, err := r.ListSpeakers(ctx)
	if err != nil {
		return nil, err
	}

	matches := make(map[string]models.Speaker)
	for label, embedding := range embeddings {
		if len(embedding) == 0 {
			continue
		}
		emb := l2Normalize(embedding)
		encoded, err := encodeEmbedding(emb)
		if err != nil {
			return nil, fmt.Errorf("encode embedding for %s: %w", label, err)
		}

		vp := models.SpeakerVoiceprint{
			TranscriptionJobID: jobID,
			OriginalSpeaker:    label,
			Embedding:          encoded,
		}

		// Match against library centroids
		bestSim := -1.0
		var best *models.Speaker
		for i := range speakers {
			centroid, err := decodeEmbedding(speakers[i].Centroid)
			if err != nil {
				continue
			}
			if sim := cosineSimilarity(emb, centroid); sim > bestSim {
				bestSim = sim
				best = &speakers[i]
			}
		}
		if best != nil && bestSim >= r.threshold {
			vp.MatchedSpeakerID = &best.ID
			vp.Confidence = &bestSim
			matches[label] = *best
			logger.Info("Voice library match", "job_id", jobID, "label", label,
				"speaker", best.Name, "confidence", fmt.Sprintf("%.3f", bestSim))
		}

		// Idempotent per (job, label): replace any previous voiceprint
		if err := r.db.WithContext(ctx).
			Where("transcription_job_id = ? AND original_speaker = ?", jobID, label).
			Delete(&models.SpeakerVoiceprint{}).Error; err != nil {
			return nil, err
		}
		if err := r.db.WithContext(ctx).Create(&vp).Error; err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func (r *voiceLibraryRepository) LearnFromJob(ctx context.Context, jobID, originalSpeaker, name string) error {
	var vp models.SpeakerVoiceprint
	err := r.db.WithContext(ctx).
		Where("transcription_job_id = ? AND original_speaker = ?", jobID, originalSpeaker).
		First(&vp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // no embedding captured for this label — nothing to learn
	}
	if err != nil {
		return err
	}
	embedding, err := decodeEmbedding(vp.Embedding)
	if err != nil {
		return fmt.Errorf("decode voiceprint embedding: %w", err)
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var speaker models.Speaker
		err := tx.Where("name = ?", name).First(&speaker).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			encoded, err := encodeEmbedding(l2Normalize(embedding))
			if err != nil {
				return err
			}
			speaker = models.Speaker{Name: name, Centroid: encoded, SampleCount: 1}
			if err := tx.Create(&speaker).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			centroid, err := decodeEmbedding(speaker.Centroid)
			if err != nil || len(centroid) != len(embedding) {
				// Corrupt/mismatched centroid: restart from this embedding
				centroid = nil
			}
			var updated []float32
			if centroid == nil {
				updated = l2Normalize(embedding)
				speaker.SampleCount = 1
			} else {
				n := float64(speaker.SampleCount)
				merged := make([]float32, len(centroid))
				for i := range centroid {
					merged[i] = float32((float64(centroid[i])*n + float64(embedding[i])) / (n + 1))
				}
				updated = l2Normalize(merged)
				speaker.SampleCount++
			}
			encoded, err := encodeEmbedding(updated)
			if err != nil {
				return err
			}
			speaker.Centroid = encoded
			if err := tx.Save(&speaker).Error; err != nil {
				return err
			}
		}

		// Point the voiceprint at its confirmed speaker (confidence 1.0 = human)
		confirmed := 1.0
		return tx.Model(&models.SpeakerVoiceprint{}).
			Where("id = ?", vp.ID).
			Updates(map[string]interface{}{"matched_speaker_id": speaker.ID, "confidence": confirmed}).Error
	})
}

func (r *voiceLibraryRepository) SuggestionsForJob(ctx context.Context, jobID string) (map[string]MatchSuggestion, error) {
	var prints []models.SpeakerVoiceprint
	if err := r.db.WithContext(ctx).Preload("MatchedSpeaker").
		Where("transcription_job_id = ? AND matched_speaker_id IS NOT NULL", jobID).
		Find(&prints).Error; err != nil {
		return nil, err
	}
	out := make(map[string]MatchSuggestion, len(prints))
	for _, p := range prints {
		if p.MatchedSpeaker == nil || p.Confidence == nil {
			continue
		}
		out[p.OriginalSpeaker] = MatchSuggestion{
			SpeakerID:  p.MatchedSpeaker.ID,
			Name:       p.MatchedSpeaker.Name,
			Confidence: *p.Confidence,
		}
	}
	return out, nil
}

func (r *voiceLibraryRepository) ApplyMatchesAsMappings(ctx context.Context, jobID string, matches map[string]models.Speaker) error {
	for label, speaker := range matches {
		var existing models.SpeakerMapping
		err := r.db.WithContext(ctx).
			Where("transcription_job_id = ? AND original_speaker = ?", jobID, label).
			First(&existing).Error
		if err == nil {
			continue // human mapping (or earlier match) already present
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		mapping := models.SpeakerMapping{
			TranscriptionJobID: jobID,
			OriginalSpeaker:    label,
			CustomName:         speaker.Name,
		}
		if err := r.db.WithContext(ctx).Create(&mapping).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *voiceLibraryRepository) ListSpeakers(ctx context.Context) ([]models.Speaker, error) {
	var speakers []models.Speaker
	err := r.db.WithContext(ctx).Order("name").Find(&speakers).Error
	return speakers, err
}

func (r *voiceLibraryRepository) RenameSpeaker(ctx context.Context, id uint, name string) error {
	return r.db.WithContext(ctx).Model(&models.Speaker{}).Where("id = ?", id).Update("name", name).Error
}

func (r *voiceLibraryRepository) DeleteSpeaker(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&models.Speaker{}, id).Error
}

func (r *voiceLibraryRepository) MergeSpeakers(ctx context.Context, fromID, toID uint) error {
	if fromID == toID {
		return fmt.Errorf("cannot merge a speaker into itself")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var from, to models.Speaker
		if err := tx.First(&from, fromID).Error; err != nil {
			return err
		}
		if err := tx.First(&to, toID).Error; err != nil {
			return err
		}
		fromC, err1 := decodeEmbedding(from.Centroid)
		toC, err2 := decodeEmbedding(to.Centroid)
		if err1 == nil && err2 == nil && len(fromC) == len(toC) {
			fn, tn := float64(from.SampleCount), float64(to.SampleCount)
			merged := make([]float32, len(toC))
			for i := range toC {
				merged[i] = float32((float64(toC[i])*tn + float64(fromC[i])*fn) / (tn + fn))
			}
			encoded, err := encodeEmbedding(l2Normalize(merged))
			if err != nil {
				return err
			}
			to.Centroid = encoded
			to.SampleCount += from.SampleCount
			if err := tx.Save(&to).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&models.SpeakerVoiceprint{}).
			Where("matched_speaker_id = ?", fromID).
			Update("matched_speaker_id", toID).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Speaker{}, fromID).Error
	})
}
