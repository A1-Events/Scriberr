package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"scriberr/internal/models"
)

// Company/project grouping endpoints. Mirrors the voice-library handlers: the
// repository is optional, so a deployment that never wires it simply 404s
// instead of panicking.

func (h *Handler) taggingReady(c *gin.Context) bool {
	if h.taggingRepo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Tagging is not enabled"})
		return false
	}
	return true
}

// ListCompanies godoc
// @Summary List companies
// @Tags tagging
// @Success 200 {array} models.Company
// @Router /api/v1/companies [get]
func (h *Handler) ListCompanies(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	items, err := h.taggingRepo.ListCompanies(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

// CreateCompany godoc
// @Summary Create a company
// @Tags tagging
// @Router /api/v1/companies [post]
func (h *Handler) CreateCompany(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	var req struct {
		Key  string `json:"key" binding:"required"`
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item := models.Company{Key: req.Key, Name: req.Name}
	if err := h.taggingRepo.CreateCompany(c.Request.Context(), &item); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, item)
}

// UpdateCompany godoc
// @Summary Rename a company
// @Tags tagging
// @Router /api/v1/companies/{id} [put]
func (h *Handler) UpdateCompany(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return
	}
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.taggingRepo.UpdateCompany(c.Request.Context(), id, req.Name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

// DeleteCompany godoc
// @Summary Delete a company (its recordings become unclassified)
// @Tags tagging
// @Router /api/v1/companies/{id} [delete]
func (h *Handler) DeleteCompany(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return
	}
	if err := h.taggingRepo.DeleteCompany(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ListTags godoc
// @Summary List project tags
// @Tags tagging
// @Router /api/v1/tags [get]
func (h *Handler) ListTags(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	items, err := h.taggingRepo.ListTags(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

// CreateTag godoc
// @Summary Create a project tag
// @Tags tagging
// @Router /api/v1/tags [post]
func (h *Handler) CreateTag(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	var req struct {
		Key       string `json:"key" binding:"required"`
		Name      string `json:"name" binding:"required"`
		CompanyID *uint  `json:"company_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item := models.Tag{Key: req.Key, Name: req.Name, CompanyID: req.CompanyID}
	if err := h.taggingRepo.CreateTag(c.Request.Context(), &item); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, item)
}

// UpdateTag godoc
// @Summary Rename or re-scope a tag
// @Tags tagging
// @Router /api/v1/tags/{id} [put]
func (h *Handler) UpdateTag(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return
	}
	var req struct {
		Name      string `json:"name" binding:"required"`
		CompanyID *uint  `json:"company_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.taggingRepo.UpdateTag(c.Request.Context(), id, req.Name, req.CompanyID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

// GetTagUsage godoc
// @Summary How many recordings carry this tag
// @Tags tagging
// @Router /api/v1/tags/{id}/usage [get]
func (h *Handler) GetTagUsage(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return
	}
	n, err := h.taggingRepo.TagUsage(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tag_id": id, "recordings": n})
}

// DeleteTag godoc
// @Summary Delete a tag, optionally moving its recordings to another one
// @Description Pass ?reassign_to=<tagID> to re-point affected recordings instead of dropping the label.
// @Tags tagging
// @Router /api/v1/tags/{id} [delete]
func (h *Handler) DeleteTag(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return
	}
	var reassignTo *uint
	if raw := c.Query("reassign_to"); raw != "" {
		v, convErr := strconv.ParseUint(raw, 10, 64)
		if convErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid reassign_to"})
			return
		}
		u := uint(v)
		reassignTo = &u
	}
	if err := h.taggingRepo.DeleteTag(c.Request.Context(), id, reassignTo); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// GetTranscriptionLabels godoc
// @Summary Get a recording's company and tags
// @Tags tagging
// @Router /api/v1/transcription/{id}/labels [get]
func (h *Handler) GetTranscriptionLabels(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	jobID := c.Param("id")
	companyID, links, err := h.taggingRepo.LabelsForJob(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"company_id": companyID, "tags": links})
}

// UpdateTranscriptionLabels godoc
// @Summary Set a recording's company and tags (marks them as a human correction)
// @Tags tagging
// @Router /api/v1/transcription/{id}/labels [put]
func (h *Handler) UpdateTranscriptionLabels(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	jobID := c.Param("id")
	var req struct {
		CompanyID *uint  `json:"company_id"`
		TagIDs    []uint `json:"tag_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Anything set through this endpoint came from a human, which is exactly
	// what the classifier later learns from.
	if err := h.taggingRepo.SetLabels(c.Request.Context(), jobID, req.CompanyID, req.TagIDs, models.SourceManual, nil); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func parseUintParam(c *gin.Context, name string) (uint, error) {
	v, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + name})
		return 0, err
	}
	return uint(v), nil
}

// BackfillClassification godoc
// @Summary Classify finished recordings that have no company yet
// @Description Uses each recording's STORED transcript, so nothing is re-transcribed. Labels are written as machine guesses (source=auto), never as human corrections.
// @Tags tagging
// @Router /api/v1/tags/backfill [post]
func (h *Handler) BackfillClassification(c *gin.Context) {
	if !h.taggingReady(c) {
		return
	}
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			limit = v
		}
	}
	classified, skipped, err := h.unifiedProcessor.GetUnifiedService().ClassifyExisting(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"classified": classified, "skipped": skipped})
}
