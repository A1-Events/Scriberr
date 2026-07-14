package models

import "time"

// Speaker is a globally known voice in the voice library. Its centroid is the
// running mean of all embeddings that were confirmed to belong to this person
// (L2-normalized, stored as a JSON-encoded []float32).
type Speaker struct {
	ID          uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string    `json:"name" gorm:"type:varchar(100);not null;uniqueIndex"`
	Centroid    []byte    `json:"-" gorm:"type:blob;not null"`
	SampleCount int       `json:"sample_count" gorm:"not null;default:0"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// SpeakerVoiceprint is the voice embedding of one diarized speaker label in
// one recording, plus the library match (if any) made at processing time.
type SpeakerVoiceprint struct {
	ID                 uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	TranscriptionJobID string    `json:"transcription_job_id" gorm:"type:varchar(36);not null;index;uniqueIndex:idx_voiceprint_job_speaker,priority:1"`
	OriginalSpeaker    string    `json:"original_speaker" gorm:"type:varchar(50);not null;uniqueIndex:idx_voiceprint_job_speaker,priority:2"`
	Embedding          []byte    `json:"-" gorm:"type:blob;not null"`
	MatchedSpeakerID   *uint     `json:"matched_speaker_id" gorm:"index"`
	Confidence         *float64  `json:"confidence"`
	CreatedAt          time.Time `json:"created_at" gorm:"autoCreateTime"`

	TranscriptionJob TranscriptionJob `json:"-" gorm:"foreignKey:TranscriptionJobID;constraint:OnDelete:CASCADE"`
	MatchedSpeaker   *Speaker         `json:"matched_speaker,omitempty" gorm:"foreignKey:MatchedSpeakerID;constraint:OnDelete:SET NULL"`
}
