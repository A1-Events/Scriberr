package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// VoiceLibraryRenameRequest renames a library speaker.
type VoiceLibraryRenameRequest struct {
	Name string `json:"name" binding:"required"`
}

// VoiceLibraryMergeRequest merges speaker `from_id` into `to_id`.
type VoiceLibraryMergeRequest struct {
	FromID uint `json:"from_id" binding:"required"`
	ToID   uint `json:"to_id" binding:"required"`
}

func (h *Handler) voiceLibraryEnabled(c *gin.Context) bool {
	if h.voiceLibraryRepo == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "Voice library is not enabled"})
		return false
	}
	return true
}

// ListVoiceLibrarySpeakers lists all known voices.
// @Router /api/v1/voice-library [get]
func (h *Handler) ListVoiceLibrarySpeakers(c *gin.Context) {
	if !h.voiceLibraryEnabled(c) {
		return
	}
	speakers, err := h.voiceLibraryRepo.ListSpeakers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list speakers"})
		return
	}
	c.JSON(http.StatusOK, speakers)
}

// RenameVoiceLibrarySpeaker renames a known voice.
// @Router /api/v1/voice-library/{id} [put]
func (h *Handler) RenameVoiceLibrarySpeaker(c *gin.Context) {
	if !h.voiceLibraryEnabled(c) {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid speaker id"})
		return
	}
	var req VoiceLibraryRenameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}
	if err := h.voiceLibraryRepo.RenameSpeaker(c.Request.Context(), uint(id), req.Name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to rename speaker"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Speaker renamed"})
}

// DeleteVoiceLibrarySpeaker removes a known voice (its voiceprints stay,
// unmatched).
// @Router /api/v1/voice-library/{id} [delete]
func (h *Handler) DeleteVoiceLibrarySpeaker(c *gin.Context) {
	if !h.voiceLibraryEnabled(c) {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid speaker id"})
		return
	}
	if err := h.voiceLibraryRepo.DeleteSpeaker(c.Request.Context(), uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete speaker"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Speaker deleted"})
}

// MergeVoiceLibrarySpeakers merges two known voices (same person enrolled twice).
// @Router /api/v1/voice-library/merge [post]
func (h *Handler) MergeVoiceLibrarySpeakers(c *gin.Context) {
	if !h.voiceLibraryEnabled(c) {
		return
	}
	var req VoiceLibraryMergeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}
	if err := h.voiceLibraryRepo.MergeSpeakers(c.Request.Context(), req.FromID, req.ToID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to merge speakers: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Speakers merged"})
}
