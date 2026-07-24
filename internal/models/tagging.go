package models

import "time"

// Grouping model for the recording library. A recording belongs to exactly ONE
// company (the business context it happened in) and carries ANY NUMBER of tags
// (the projects/topics it touched) — a meeting can legitimately cover two
// projects at once, so tags are many-to-many while the company is not.
//
// Assignments are made by the LLM classifier and can be overridden by hand.
// The distinction is carried by Source: a `manual` row IS a user correction,
// which is what the classifier later learns from. There is deliberately no
// separate "training" table to drift out of sync with reality.

// AssignmentSource records who decided a label.
const (
	SourceAuto   = "auto"
	SourceManual = "manual"
)

// Company is the top-level bucket: one per recording.
type Company struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Key       string    `json:"key" gorm:"type:varchar(64);not null;uniqueIndex"`
	Name      string    `json:"name" gorm:"type:varchar(120);not null"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// Tag is a project/topic label. CompanyID scopes it to one company; NULL means
// it may be applied to any company's recordings.
type Tag struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Key       string    `json:"key" gorm:"type:varchar(64);not null;uniqueIndex"`
	Name      string    `json:"name" gorm:"type:varchar(120);not null"`
	CompanyID *uint     `json:"company_id" gorm:"index"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	Company *Company `json:"company,omitempty" gorm:"foreignKey:CompanyID;constraint:OnDelete:SET NULL"`
}

// JobTag links a recording to one tag. Unique per (job, tag) so re-classifying
// is idempotent. Deleting the job removes its links; deleting a tag is handled
// explicitly by the API so the user can choose where affected recordings go.
type JobTag struct {
	ID                 uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	TranscriptionJobID string    `json:"transcription_job_id" gorm:"type:varchar(36);not null;index;uniqueIndex:idx_jobtag_job_tag,priority:1"`
	TagID              uint      `json:"tag_id" gorm:"not null;index;uniqueIndex:idx_jobtag_job_tag,priority:2"`
	Source             string    `json:"source" gorm:"type:varchar(10);not null;default:'auto'"`
	Confidence         *float64  `json:"confidence"`
	CreatedAt          time.Time `json:"created_at" gorm:"autoCreateTime"`

	TranscriptionJob TranscriptionJob `json:"-" gorm:"foreignKey:TranscriptionJobID;constraint:OnDelete:CASCADE"`
	Tag              Tag              `json:"tag,omitempty" gorm:"foreignKey:TagID;constraint:OnDelete:CASCADE"`
}
