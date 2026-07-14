package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/core-team-builder/backend/internal/models"
)

const (
	// maxRosterImagesPerRoster caps how many positioning images a roster may hold.
	maxRosterImagesPerRoster = 10
	// maxImageCaptionLen caps an image caption (in runes).
	maxImageCaptionLen = 200
	// maxImageBytes caps a single uploaded image. Kept under maxImageUploadBody
	// (the per-route request cap) so the multipart envelope also fits.
	maxImageBytes = 5 << 20 // 5 MiB
)

// allowedImageTypes are the MIME types accepted for positioning images — the
// common raster formats Discord renders inline. The type is sniffed from the
// bytes, not trusted from the client.
var allowedImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// rosterImageAccess resolves the team (and caller role) and the {imgID} image,
// ensuring the image belongs to the team. It writes the error response and
// returns ok=false on any failure.
func (s *Server) rosterImageAccess(w http.ResponseWriter, r *http.Request) (teamID int64, role string, image *models.RosterImage, ok bool) {
	teamID, _, role, ok = s.teamAccess(w, r)
	if !ok {
		return 0, "", nil, false
	}
	iid, err := strconv.ParseInt(r.PathValue("imgID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid image id")
		return 0, "", nil, false
	}
	image, err = s.rosterImages.Get(r.Context(), iid)
	if errors.Is(err, models.ErrRosterImageNotFound) {
		writeError(w, http.StatusNotFound, "image not found")
		return 0, "", nil, false
	}
	if err != nil {
		log.Printf("get roster image: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load image")
		return 0, "", nil, false
	}
	if image.TeamID != teamID {
		writeError(w, http.StatusNotFound, "image not found")
		return 0, "", nil, false
	}
	return teamID, role, image, true
}

func (s *Server) handleListRosterImages(w http.ResponseWriter, r *http.Request) {
	teamID, _, _, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	rosterID, ok := s.resolveRoster(w, r, teamID)
	if !ok {
		return
	}
	images, err := s.rosterImages.ListForRoster(r.Context(), rosterID)
	if err != nil {
		log.Printf("list roster images: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load images")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": images})
}

func (s *Server) handleUploadRosterImage(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}
	rosterID, ok := s.resolveRoster(w, r, teamID)
	if !ok {
		return
	}

	// The overall body is capped by withMaxBytes (which grants this route a
	// larger budget); bound the in-memory multipart parsing too.
	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload")
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing image file")
		return
	}
	defer file.Close()

	// Read one byte past the cap so we can detect (and reject) oversize uploads.
	data, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read upload")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty image file")
		return
	}
	if len(data) > maxImageBytes {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("image too large (max %d MB)", maxImageBytes>>20))
		return
	}

	// Sniff the real content type; only accept genuine image formats.
	contentType := http.DetectContentType(data)
	if !allowedImageTypes[contentType] {
		writeError(w, http.StatusBadRequest, "unsupported image type (use PNG, JPEG, GIF, or WebP)")
		return
	}

	caption := strings.TrimSpace(r.FormValue("caption"))
	if len([]rune(caption)) > maxImageCaptionLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("caption too long (max %d characters)", maxImageCaptionLen))
		return
	}

	n, err := s.rosterImages.CountForRoster(r.Context(), rosterID)
	if err != nil {
		log.Printf("count roster images: %v", err)
		writeError(w, http.StatusInternalServerError, "could not upload image")
		return
	}
	if n >= maxRosterImagesPerRoster {
		writeError(w, http.StatusConflict, fmt.Sprintf("image limit reached (max %d)", maxRosterImagesPerRoster))
		return
	}

	image, err := s.rosterImages.Create(r.Context(), rosterID, caption, contentType, data)
	if err != nil {
		log.Printf("create roster image: %v", err)
		writeError(w, http.StatusInternalServerError, "could not upload image")
		return
	}
	writeJSON(w, http.StatusCreated, image)
}

type rosterImageUpdateRequest struct {
	Caption string `json:"caption"`
}

func (s *Server) handleUpdateRosterImage(w http.ResponseWriter, r *http.Request) {
	_, role, image, ok := s.rosterImageAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}
	var req rosterImageUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	caption := strings.TrimSpace(req.Caption)
	if len([]rune(caption)) > maxImageCaptionLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("caption too long (max %d characters)", maxImageCaptionLen))
		return
	}
	if err := s.rosterImages.UpdateCaption(r.Context(), image.ID, caption); err != nil {
		log.Printf("update roster image: %v", err)
		writeError(w, http.StatusInternalServerError, "could not save caption")
		return
	}
	updated, err := s.rosterImages.Get(r.Context(), image.ID)
	if err != nil {
		log.Printf("reload roster image: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load image")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteRosterImage(w http.ResponseWriter, r *http.Request) {
	_, role, image, ok := s.rosterImageAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}
	if err := s.rosterImages.Delete(r.Context(), image.ID); err != nil {
		log.Printf("delete roster image: %v", err)
		writeError(w, http.StatusInternalServerError, "could not delete image")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetRosterImageRaw serves an image's bytes. It is bearer-protected like
// every other API route, so the frontend fetches it with auth and renders the
// result via an object URL rather than a plain <img src>.
func (s *Server) handleGetRosterImageRaw(w http.ResponseWriter, r *http.Request) {
	_, _, image, ok := s.rosterImageAccess(w, r)
	if !ok {
		return
	}
	data, contentType, err := s.rosterImages.GetData(r.Context(), image.ID)
	if err != nil {
		log.Printf("get roster image data: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load image")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		log.Printf("write roster image (image %d): %v", image.ID, err)
	}
}
