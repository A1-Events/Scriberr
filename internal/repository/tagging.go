package repository

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"scriberr/internal/models"
)

// TaggingRepository owns the company/project grouping of recordings.
//
// A recording has exactly one company and any number of tags. Assignments carry
// a Source: `manual` rows are user corrections, and those are what the
// classifier learns from — CorrectedExamples is the read side of that loop.
type TaggingRepository interface {
	ListCompanies(ctx context.Context) ([]models.Company, error)
	CreateCompany(ctx context.Context, c *models.Company) error
	UpdateCompany(ctx context.Context, id uint, name string) error
	DeleteCompany(ctx context.Context, id uint) error

	ListTags(ctx context.Context) ([]models.Tag, error)
	CreateTag(ctx context.Context, t *models.Tag) error
	UpdateTag(ctx context.Context, id uint, name string, companyID *uint) error
	// TagUsage counts how many recordings carry the tag, so the UI can warn
	// before deleting and offer to move them somewhere specific.
	TagUsage(ctx context.Context, id uint) (int64, error)
	// DeleteTag removes the tag. When reassignTo is non-nil the affected
	// recordings are re-pointed at that tag instead of simply losing the label.
	DeleteTag(ctx context.Context, id uint, reassignTo *uint) error

	// SetLabels replaces a job's company and tags in one transaction.
	SetLabels(ctx context.Context, jobID string, companyID *uint, tagIDs []uint, source string, confidence *float64) error
	LabelsForJob(ctx context.Context, jobID string) (*uint, []models.JobTag, error)
	// CorrectedExamples returns the most recent manual assignments, newest
	// first — the few-shot material for the classifier.
	CorrectedExamples(ctx context.Context, limit int) ([]CorrectedExample, error)
}

// CorrectedExample is one recording the user labelled by hand.
type CorrectedExample struct {
	JobID      string
	Title      string
	Transcript string
	CompanyKey string
	TagKeys    []string
}

type taggingRepository struct{ db *gorm.DB }

func NewTaggingRepository(db *gorm.DB) TaggingRepository { return &taggingRepository{db: db} }

func (r *taggingRepository) ListCompanies(ctx context.Context) ([]models.Company, error) {
	var out []models.Company
	return out, r.db.WithContext(ctx).Order("name").Find(&out).Error
}

func (r *taggingRepository) CreateCompany(ctx context.Context, c *models.Company) error {
	return r.db.WithContext(ctx).Create(c).Error
}

func (r *taggingRepository) UpdateCompany(ctx context.Context, id uint, name string) error {
	return r.db.WithContext(ctx).Model(&models.Company{}).Where("id = ?", id).
		Update("name", name).Error
}

// DeleteCompany clears the company off its recordings (FK is ON DELETE SET
// NULL) rather than deleting them; its tags become global.
func (r *taggingRepository) DeleteCompany(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.TranscriptionJob{}).Where("company_id = ?", id).
			Updates(map[string]any{"company_id": nil, "company_source": nil, "company_confidence": nil}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Tag{}).Where("company_id = ?", id).
			Update("company_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Company{}, id).Error
	})
}

func (r *taggingRepository) ListTags(ctx context.Context) ([]models.Tag, error) {
	var out []models.Tag
	return out, r.db.WithContext(ctx).Order("name").Find(&out).Error
}

func (r *taggingRepository) CreateTag(ctx context.Context, t *models.Tag) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *taggingRepository) UpdateTag(ctx context.Context, id uint, name string, companyID *uint) error {
	return r.db.WithContext(ctx).Model(&models.Tag{}).Where("id = ?", id).
		Updates(map[string]any{"name": name, "company_id": companyID}).Error
}

func (r *taggingRepository) TagUsage(ctx context.Context, id uint) (int64, error) {
	var n int64
	return n, r.db.WithContext(ctx).Model(&models.JobTag{}).Where("tag_id = ?", id).Count(&n).Error
}

func (r *taggingRepository) DeleteTag(ctx context.Context, id uint, reassignTo *uint) error {
	if reassignTo != nil && *reassignTo == id {
		return fmt.Errorf("cannot reassign a tag to itself")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if reassignTo != nil {
			// Move the affected recordings onto the replacement. Skip any that
			// already carry it, otherwise the (job, tag) unique index trips.
			var links []models.JobTag
			if err := tx.Where("tag_id = ?", id).Find(&links).Error; err != nil {
				return err
			}
			for _, l := range links {
				var existing int64
				if err := tx.Model(&models.JobTag{}).
					Where("transcription_job_id = ? AND tag_id = ?", l.TranscriptionJobID, *reassignTo).
					Count(&existing).Error; err != nil {
					return err
				}
				if existing > 0 {
					continue
				}
				if err := tx.Model(&models.JobTag{}).Where("id = ?", l.ID).
					Update("tag_id", *reassignTo).Error; err != nil {
					return err
				}
			}
		}
		// Anything still pointing here goes away with the tag.
		if err := tx.Where("tag_id = ?", id).Delete(&models.JobTag{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.Tag{}, id).Error
	})
}

func (r *taggingRepository) SetLabels(ctx context.Context, jobID string, companyID *uint, tagIDs []uint, source string, confidence *float64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.TranscriptionJob{}).Where("id = ?", jobID).
			Updates(map[string]any{
				"company_id":         companyID,
				"company_source":     source,
				"company_confidence": confidence,
			}).Error; err != nil {
			return err
		}
		if err := tx.Where("transcription_job_id = ?", jobID).Delete(&models.JobTag{}).Error; err != nil {
			return err
		}
		for _, tid := range tagIDs {
			if err := tx.Create(&models.JobTag{
				TranscriptionJobID: jobID,
				TagID:              tid,
				Source:             source,
				Confidence:         confidence,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *taggingRepository) LabelsForJob(ctx context.Context, jobID string) (*uint, []models.JobTag, error) {
	var job models.TranscriptionJob
	if err := r.db.WithContext(ctx).Select("company_id").Where("id = ?", jobID).First(&job).Error; err != nil {
		return nil, nil, err
	}
	var links []models.JobTag
	if err := r.db.WithContext(ctx).Preload("Tag").Where("transcription_job_id = ?", jobID).Find(&links).Error; err != nil {
		return nil, nil, err
	}
	return job.CompanyID, links, nil
}

func (r *taggingRepository) CorrectedExamples(ctx context.Context, limit int) ([]CorrectedExample, error) {
	var jobs []models.TranscriptionJob
	if err := r.db.WithContext(ctx).
		Where("company_source = ? AND company_id IS NOT NULL", models.SourceManual).
		Order("updated_at DESC").Limit(limit).Find(&jobs).Error; err != nil {
		return nil, err
	}
	out := make([]CorrectedExample, 0, len(jobs))
	for _, j := range jobs {
		ex := CorrectedExample{JobID: j.ID}
		if j.Title != nil {
			ex.Title = *j.Title
		}
		if j.Transcript != nil {
			ex.Transcript = *j.Transcript
		}
		var comp models.Company
		if err := r.db.WithContext(ctx).First(&comp, *j.CompanyID).Error; err == nil {
			ex.CompanyKey = comp.Key
		}
		var links []models.JobTag
		if err := r.db.WithContext(ctx).Preload("Tag").
			Where("transcription_job_id = ?", j.ID).Find(&links).Error; err == nil {
			for _, l := range links {
				if l.Tag.Key != "" {
					ex.TagKeys = append(ex.TagKeys, l.Tag.Key)
				}
			}
		}
		out = append(out, ex)
	}
	return out, nil
}
