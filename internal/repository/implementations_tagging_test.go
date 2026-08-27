package repository

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"scriberr/internal/models"
)

func TestListWithParamsFiltersAndPreloadsLabels(t *testing.T) {
	db := newTaggingTestDB(t)

	companyA := models.Company{Key: "A", Name: "Company A"}
	companyB := models.Company{Key: "B", Name: "Company B"}
	require.NoError(t, db.Create(&companyA).Error)
	require.NoError(t, db.Create(&companyB).Error)
	projectA := models.Tag{Key: "A-PROJECT", Name: "A Project", CompanyID: &companyA.ID}
	projectB := models.Tag{Key: "B-PROJECT", Name: "B Project", CompanyID: &companyB.ID}
	require.NoError(t, db.Create(&projectA).Error)
	require.NoError(t, db.Create(&projectB).Error)

	jobs := []models.TranscriptionJob{
		{ID: "job-a-project", AudioPath: "/audio/a-project.mp3", CompanyID: &companyA.ID},
		{ID: "job-a-general", AudioPath: "/audio/a-general.mp3", CompanyID: &companyA.ID},
		{ID: "job-b-project", AudioPath: "/audio/b-project.mp3", CompanyID: &companyB.ID},
	}
	for i := range jobs {
		require.NoError(t, db.Create(&jobs[i]).Error)
	}
	require.NoError(t, db.Create(&models.JobTag{
		TranscriptionJobID: jobs[0].ID,
		TagID:              projectA.ID,
		Source:             models.SourceManual,
	}).Error)
	require.NoError(t, db.Create(&models.JobTag{
		TranscriptionJobID: jobs[2].ID,
		TagID:              projectB.ID,
		Source:             models.SourceAuto,
	}).Error)

	repo := NewJobRepository(db)
	companyJobs, total, err := repo.ListWithParams(
		context.Background(), 0, 20, "", "", "", nil, JobListFilters{CompanyID: &companyA.ID},
	)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, companyJobs, 2)
	for _, job := range companyJobs {
		require.NotNil(t, job.Company)
		require.Equal(t, companyA.ID, job.Company.ID)
	}

	projectJobs, total, err := repo.ListWithParams(
		context.Background(), 0, 20, "", "", "", nil, JobListFilters{TagID: &projectA.ID},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, projectJobs, 1)
	require.Equal(t, jobs[0].ID, projectJobs[0].ID)
	require.Len(t, projectJobs[0].Tags, 1)
	require.Equal(t, projectA.ID, projectJobs[0].Tags[0].Tag.ID)
}

func TestFindByStatusPrioritizesUnattemptedClassificationCandidates(t *testing.T) {
	db := newTaggingTestDB(t)
	oldAttempt := time.Now().Add(-time.Hour)
	newAttempt := time.Now()
	jobs := []models.TranscriptionJob{
		{ID: "attempted-new", AudioPath: "/audio/new.mp3", Status: models.StatusCompleted, ClassificationAttemptedAt: &newAttempt},
		{ID: "unattempted", AudioPath: "/audio/unattempted.mp3", Status: models.StatusCompleted},
		{ID: "attempted-old", AudioPath: "/audio/old.mp3", Status: models.StatusCompleted, ClassificationAttemptedAt: &oldAttempt},
	}
	for i := range jobs {
		require.NoError(t, db.Create(&jobs[i]).Error)
	}

	got, err := NewJobRepository(db).FindByStatus(context.Background(), models.StatusCompleted)
	require.NoError(t, err)
	require.Equal(t, []string{"unattempted", "attempted-old", "attempted-new"}, []string{got[0].ID, got[1].ID, got[2].ID})

	require.NoError(t, NewJobRepository(db).MarkClassificationAttempted(context.Background(), "unattempted"))
	got, err = NewJobRepository(db).FindByStatus(context.Background(), models.StatusCompleted)
	require.NoError(t, err)
	require.Equal(t, []string{"attempted-old", "attempted-new", "unattempted"}, []string{got[0].ID, got[1].ID, got[2].ID})
}

func TestSetLabelsRejectsCrossCompanyProjectsAndDeduplicates(t *testing.T) {
	db := newTaggingTestDB(t)
	companyA := models.Company{Key: "A", Name: "Company A"}
	companyB := models.Company{Key: "B", Name: "Company B"}
	require.NoError(t, db.Create(&companyA).Error)
	require.NoError(t, db.Create(&companyB).Error)
	projectA := models.Tag{Key: "A-PROJECT", Name: "A Project", CompanyID: &companyA.ID}
	projectB := models.Tag{Key: "B-PROJECT", Name: "B Project", CompanyID: &companyB.ID}
	require.NoError(t, db.Create(&projectA).Error)
	require.NoError(t, db.Create(&projectB).Error)
	job := models.TranscriptionJob{ID: "job", AudioPath: "/audio/job.mp3"}
	require.NoError(t, db.Create(&job).Error)

	repo := NewTaggingRepository(db)
	err := repo.SetLabels(context.Background(), job.ID, &companyA.ID, []uint{projectB.ID}, models.SourceManual, nil)
	require.ErrorContains(t, err, "does not belong")

	require.NoError(t, repo.SetLabels(
		context.Background(), job.ID, &companyA.ID, []uint{projectA.ID, projectA.ID}, models.SourceManual, nil,
	))
	companyID, companySource, links, err := repo.LabelsForJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Equal(t, companyA.ID, *companyID)
	require.Equal(t, models.SourceManual, *companySource)
	require.Len(t, links, 1)
	require.Equal(t, projectA.ID, links[0].TagID)

	confidence := 0.9
	err = repo.SetLabels(
		context.Background(), job.ID, &companyB.ID, []uint{projectB.ID}, models.SourceAuto, &confidence,
	)
	require.ErrorContains(t, err, "set manually")
	companyID, companySource, links, err = repo.LabelsForJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Equal(t, companyA.ID, *companyID)
	require.Equal(t, models.SourceManual, *companySource)
	require.Len(t, links, 1)
	require.Equal(t, projectA.ID, links[0].TagID)

	err = repo.SetLabels(context.Background(), "missing-job", nil, nil, models.SourceManual, nil)
	require.Error(t, err)
}

func TestDeleteTagReassignmentPreservesManualProvenance(t *testing.T) {
	db := newTaggingTestDB(t)
	company := models.Company{Key: "A", Name: "Company A"}
	require.NoError(t, db.Create(&company).Error)
	oldProject := models.Tag{Key: "OLD", Name: "Old", CompanyID: &company.ID}
	newProject := models.Tag{Key: "NEW", Name: "New", CompanyID: &company.ID}
	require.NoError(t, db.Create(&oldProject).Error)
	require.NoError(t, db.Create(&newProject).Error)
	job := models.TranscriptionJob{ID: "job", AudioPath: "/audio/job.mp3", CompanyID: &company.ID}
	require.NoError(t, db.Create(&job).Error)
	require.NoError(t, db.Create(&models.JobTag{
		TranscriptionJobID: job.ID, TagID: oldProject.ID, Source: models.SourceManual,
	}).Error)
	require.NoError(t, db.Create(&models.JobTag{
		TranscriptionJobID: job.ID, TagID: newProject.ID, Source: models.SourceAuto,
	}).Error)

	repo := NewTaggingRepository(db)
	require.NoError(t, repo.DeleteTag(context.Background(), oldProject.ID, &newProject.ID))
	_, _, links, err := repo.LabelsForJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, newProject.ID, links[0].TagID)
	require.Equal(t, models.SourceManual, links[0].Source)
}

func TestUpdateTagIgnoresSoftDeletedRecordingsWhenScoping(t *testing.T) {
	db := newTaggingTestDB(t)
	companyA := models.Company{Key: "A", Name: "Company A"}
	companyB := models.Company{Key: "B", Name: "Company B"}
	require.NoError(t, db.Create(&companyA).Error)
	require.NoError(t, db.Create(&companyB).Error)
	project := models.Tag{Key: "PROJECT", Name: "Project"}
	require.NoError(t, db.Create(&project).Error)
	deletedJob := models.TranscriptionJob{
		ID: "deleted-job", AudioPath: "/audio/deleted.mp3", CompanyID: &companyB.ID,
	}
	require.NoError(t, db.Create(&deletedJob).Error)
	require.NoError(t, db.Create(&models.JobTag{
		TranscriptionJobID: deletedJob.ID, TagID: project.ID, Source: models.SourceManual,
	}).Error)
	require.NoError(t, db.Delete(&deletedJob).Error)

	repo := NewTaggingRepository(db)
	require.NoError(t, repo.UpdateTag(context.Background(), project.ID, project.Name, &companyA.ID))

	var updated models.Tag
	require.NoError(t, db.First(&updated, project.ID).Error)
	require.Equal(t, companyA.ID, *updated.CompanyID)
}

func TestTaxonomyMutationsInvalidateRecordingDeltaRows(t *testing.T) {
	db := newTaggingTestDB(t)
	company := models.Company{Key: "A", Name: "Company A"}
	require.NoError(t, db.Create(&company).Error)
	project := models.Tag{Key: "PROJECT", Name: "Project", CompanyID: &company.ID}
	require.NoError(t, db.Create(&project).Error)
	job := models.TranscriptionJob{ID: "job", AudioPath: "/audio/job.mp3", CompanyID: &company.ID}
	require.NoError(t, db.Create(&job).Error)
	require.NoError(t, db.Create(&models.JobTag{
		TranscriptionJobID: job.ID, TagID: project.ID, Source: models.SourceManual,
	}).Error)
	watermark := time.Now().Add(-time.Minute)
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Where("id = ?", job.ID).
		UpdateColumn("updated_at", watermark).Error)

	repo := NewTaggingRepository(db)
	require.NoError(t, repo.UpdateTag(context.Background(), project.ID, "Renamed", &company.ID))

	jobs, total, err := NewJobRepository(db).ListWithParams(
		context.Background(), 0, 20, "", "", "", &watermark, JobListFilters{},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, jobs, 1)
	require.Equal(t, "Renamed", jobs[0].Tags[0].Tag.Name)
}

func TestTaxonomyMutationDoesNotChangeCorrectionExampleRecency(t *testing.T) {
	db := newTaggingTestDB(t)
	company := models.Company{Key: "A", Name: "Company A"}
	require.NoError(t, db.Create(&company).Error)
	oldProject := models.Tag{Key: "OLD", Name: "Old project", CompanyID: &company.ID}
	newProject := models.Tag{Key: "NEW", Name: "New project", CompanyID: &company.ID}
	require.NoError(t, db.Create(&oldProject).Error)
	require.NoError(t, db.Create(&newProject).Error)
	oldJob := models.TranscriptionJob{ID: "old-correction", AudioPath: "/audio/old.mp3"}
	newJob := models.TranscriptionJob{ID: "new-correction", AudioPath: "/audio/new.mp3"}
	require.NoError(t, db.Create(&oldJob).Error)
	require.NoError(t, db.Create(&newJob).Error)

	repo := NewTaggingRepository(db)
	require.NoError(t, repo.SetLabels(context.Background(), oldJob.ID, &company.ID, []uint{oldProject.ID}, models.SourceManual, nil))
	require.NoError(t, repo.SetLabels(context.Background(), newJob.ID, &company.ID, []uint{newProject.ID}, models.SourceManual, nil))
	oldCorrection := time.Now().Add(-time.Hour)
	newCorrection := time.Now().Add(-time.Minute)
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Where("id = ?", oldJob.ID).
		UpdateColumn("labels_corrected_at", oldCorrection).Error)
	require.NoError(t, db.Model(&models.TranscriptionJob{}).Where("id = ?", newJob.ID).
		UpdateColumn("labels_corrected_at", newCorrection).Error)

	require.NoError(t, repo.UpdateTag(context.Background(), oldProject.ID, "Renamed old project", &company.ID))
	examples, err := repo.CorrectedExamples(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, examples, 1)
	require.Equal(t, newJob.ID, examples[0].JobID)
}

func newTaggingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&models.Company{},
		&models.Tag{},
		&models.TranscriptionJob{},
		&models.JobTag{},
	))

	return db
}
