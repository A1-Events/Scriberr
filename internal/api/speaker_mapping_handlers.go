package api

import (
	"net/http"

	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/pkg/logger"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SpeakerMappingRequest represents a speaker mapping update request
type SpeakerMappingRequest struct {
	OriginalSpeaker string `json:"original_speaker" binding:"required"`
	CustomName      string `json:"custom_name" binding:"required"`
}

// SpeakerMappingsUpdateRequest represents a bulk speaker mappings update
type SpeakerMappingsUpdateRequest struct {
	Mappings []SpeakerMappingRequest `json:"mappings" binding:"required"`
}

// SpeakerMappingResponse represents a speaker mapping response. SuggestedName
// and Confidence are voice-library matches (present only when the library
// recognized this voice from previous recordings).
type SpeakerMappingResponse struct {
	ID              uint     `json:"id"`
	OriginalSpeaker string   `json:"original_speaker"`
	CustomName      string   `json:"custom_name"`
	SuggestedName   *string  `json:"suggested_name,omitempty"`
	Confidence      *float64 `json:"confidence,omitempty"`
}

// GetSpeakerMappings retrieves all speaker mappings for a transcription
// @Summary Get speaker mappings for a transcription
// @Description Retrieves all custom speaker names for a transcription job
// @Tags transcription
// @Produce json
// @Param id path string true "Transcription Job ID"
// @Success 200 {array} SpeakerMappingResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/transcription/{id}/speakers [get]
func (h *Handler) GetSpeakerMappings(c *gin.Context) {
	jobID := c.Param("id")

	// Verify the transcription job exists and has diarization enabled
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Transcription job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transcription job"})
		return
	}

	// Check if diarization was enabled or if this is a multi-track job (which also has speakers)
	// If no speaker info available, return empty array instead of error for graceful frontend handling
	if !job.Diarization && !job.Parameters.Diarize && !job.IsMultiTrack {
		c.JSON(http.StatusOK, []SpeakerMappingResponse{})
		return
	}

	// Get speaker mappings
	mappings, err := h.speakerMappingRepo.ListByJob(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get speaker mappings"})
		return
	}

	// Voice-library suggestions for this job (nil-safe when disabled)
	suggestions := map[string]repository.MatchSuggestion{}
	if h.voiceLibraryRepo != nil {
		if s, err := h.voiceLibraryRepo.SuggestionsForJob(c.Request.Context(), jobID); err == nil {
			suggestions = s
		}
	}

	// Convert to response format
	response := make([]SpeakerMappingResponse, len(mappings))
	seen := make(map[string]bool, len(mappings))
	for i, mapping := range mappings {
		response[i] = SpeakerMappingResponse{
			ID:              mapping.ID,
			OriginalSpeaker: mapping.OriginalSpeaker,
			CustomName:      mapping.CustomName,
		}
		seen[mapping.OriginalSpeaker] = true
		if s, ok := suggestions[mapping.OriginalSpeaker]; ok {
			name, conf := s.Name, s.Confidence
			response[i].SuggestedName = &name
			response[i].Confidence = &conf
		}
	}
	// Surface suggestions for labels the user has not named yet
	for label, s := range suggestions {
		if seen[label] {
			continue
		}
		name, conf := s.Name, s.Confidence
		response = append(response, SpeakerMappingResponse{
			OriginalSpeaker: label,
			SuggestedName:   &name,
			Confidence:      &conf,
		})
	}

	c.JSON(http.StatusOK, response)
}

// UpdateSpeakerMappings updates speaker mappings for a transcription
// @Summary Update speaker mappings for a transcription
// @Description Updates or creates custom speaker names for a transcription job
// @Tags transcription
// @Accept json
// @Produce json
// @Param id path string true "Transcription Job ID"
// @Param request body SpeakerMappingsUpdateRequest true "Speaker mappings to update"
// @Success 200 {array} SpeakerMappingResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/transcription/{id}/speakers [post]
func (h *Handler) UpdateSpeakerMappings(c *gin.Context) {
	jobID := c.Param("id")

	var req SpeakerMappingsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Verify the transcription job exists and has diarization enabled
	job, err := h.jobRepo.FindByID(c.Request.Context(), jobID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Transcription job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transcription job"})
		return
	}

	// Check if diarization was enabled or if this is a multi-track job (which also has speakers)
	if !job.Diarization && !job.Parameters.Diarize && !job.IsMultiTrack {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No speaker information available for this transcription"})
		return
	}

	// Convert request to model
	var mappings []models.SpeakerMapping
	for _, mapping := range req.Mappings {
		mappings = append(mappings, models.SpeakerMapping{
			TranscriptionJobID: jobID,
			OriginalSpeaker:    mapping.OriginalSpeaker,
			CustomName:         mapping.CustomName,
		})
	}

	// Update mappings using repository
	if err := h.speakerMappingRepo.UpdateMappings(c.Request.Context(), jobID, mappings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update speaker mappings"})
		return
	}

	// Voice library learn-on-rename: fold this job's voiceprints into the
	// named speakers' centroids. Best-effort — renames must not fail on it.
	if h.voiceLibraryRepo != nil {
		learned := false
		for _, mapping := range req.Mappings {
			if mapping.CustomName == "" {
				continue
			}
			if err := h.voiceLibraryRepo.LearnFromJob(c.Request.Context(), jobID, mapping.OriginalSpeaker, mapping.CustomName); err != nil {
				logger.Warn("Voice library learn-on-rename failed",
					"job_id", jobID, "speaker", mapping.OriginalSpeaker, "error", err)
			} else {
				learned = true
			}
		}
		// The library changed — refresh suggestions on past recordings so
		// newly enrolled voices surface everywhere (suggestions only).
		if learned {
			if _, err := h.voiceLibraryRepo.ResweepSuggestions(c.Request.Context()); err != nil {
				logger.Warn("Voice library resweep failed", "error", err)
			}
		}
	}

	// Fetch updated mappings to return
	updatedMappings, err := h.speakerMappingRepo.ListByJob(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch updated mappings"})
		return
	}

	// Convert to response format
	response := make([]SpeakerMappingResponse, len(updatedMappings))
	for i, mapping := range updatedMappings {
		response[i] = SpeakerMappingResponse{
			ID:              mapping.ID,
			OriginalSpeaker: mapping.OriginalSpeaker,
			CustomName:      mapping.CustomName,
		}
	}

	c.JSON(http.StatusOK, response)
}
