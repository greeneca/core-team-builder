package handlers

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/core-team-builder/backend/internal/models"
)

// pngBytes returns something http.DetectContentType reports as image/png. The
// handler sniffs the magic number and stores the bytes verbatim — it never
// decodes the image — so a valid signature plus filler is enough.
func pngBytes(size int) []byte {
	data := make([]byte, size)
	copy(data, []byte("\x89PNG\r\n\x1a\n"))
	return data
}

// imageUploadBody builds the multipart payload the upload form posts, returning
// it alongside the Content-Type header that describes its boundary.
func imageUploadBody(t *testing.T, fieldName, caption string, data []byte) ([]byte, string) {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if fieldName != "" {
		part, err := w.CreateFormFile(fieldName, "positioning.png")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if caption != "" {
		if err := w.WriteField("caption", caption); err != nil {
			t.Fatalf("write caption field: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

// uploadImage posts a positioning image to a team's active roster.
func (a *testAPI) uploadImage(s *session, teamID int64, caption string, data []byte) *response {
	a.t.Helper()

	body, contentType := imageUploadBody(a.t, "image", caption, data)
	return a.doRaw(http.MethodPost, fmt.Sprintf("/api/teams/%d/images", teamID), s.token, contentType, body)
}

// listImages reads a roster's positioning images through the API.
func (a *testAPI) listImages(s *session, teamID int64) []models.RosterImage {
	a.t.Helper()

	var payload struct {
		Images []models.RosterImage `json:"images"`
	}
	a.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/images", teamID), s.token, nil).
		expect(http.StatusOK).
		decode(&payload)
	return payload.Images
}

// TestUploadRosterImage covers the upload round trip: the sniffed content type
// and size are recorded, the caption is kept, and the bytes come back byte-for
// byte from the raw endpoint.
func TestUploadRosterImage(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	data := pngBytes(64)
	var created models.RosterImage
	api.uploadImage(owner, team.ID, "  Boss 1 positioning  ", data).
		expect(http.StatusCreated).
		decode(&created)

	if created.Caption != "Boss 1 positioning" {
		t.Errorf("caption = %q, want it trimmed", created.Caption)
	}
	if created.ContentType != "image/png" {
		t.Errorf("content type = %q, want image/png", created.ContentType)
	}
	if created.ByteSize != len(data) {
		t.Errorf("byte size = %d, want %d", created.ByteSize, len(data))
	}
	// The bytes themselves must never ride along in the JSON — the list view
	// would otherwise carry megabytes per roster.
	if bytes.Contains(created.Data, data) {
		t.Error("the JSON response carried the image bytes, want them omitted")
	}

	if got := api.listImages(owner, team.ID); len(got) != 1 || got[0].ID != created.ID {
		t.Errorf("list = %+v, want just the image uploaded", got)
	}

	raw := api.do(http.MethodGet, fmt.Sprintf("/api/teams/%d/images/%d/raw", team.ID, created.ID), owner.token, nil).
		expect(http.StatusOK)
	if !bytes.Equal(raw.body, data) {
		t.Errorf("raw body is %d bytes, want the %d uploaded", len(raw.body), len(data))
	}
	if got := raw.header.Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	// The raw route echoes stored bytes, so browsers must be told not to
	// re-sniff them into something executable.
	if got := raw.header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// TestUploadRosterImageValidation walks the rejections. The content-type check
// is the important one: it is sniffed from the bytes, so renaming a script to
// .png does not get it stored.
func TestUploadRosterImageValidation(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d/images", team.ID)

	// Not multipart at all.
	api.doRaw(http.MethodPost, path, owner.token, "application/json", []byte(`{}`)).
		expect(http.StatusBadRequest)

	// Multipart, but with no file part.
	body, contentType := imageUploadBody(t, "", "just a caption", nil)
	api.doRaw(http.MethodPost, path, owner.token, contentType, body).
		expect(http.StatusBadRequest)

	// A file part holding nothing.
	api.uploadImage(owner, team.ID, "", nil).expect(http.StatusBadRequest)

	// Real bytes, but not an image.
	resp := api.uploadImage(owner, team.ID, "", []byte("#!/bin/sh\necho pwned\n")).
		expect(http.StatusBadRequest)
	if msg := resp.errorMessage(); !strings.Contains(msg, "unsupported image type") {
		t.Errorf("error = %q, want it to name the unsupported type", msg)
	}

	// A caption past the cap.
	api.uploadImage(owner, team.ID, strings.Repeat("c", maxImageCaptionLen+1), pngBytes(64)).
		expect(http.StatusBadRequest)

	// An image past the byte cap.
	api.uploadImage(owner, team.ID, "", pngBytes(maxImageBytes+1)).
		expect(http.StatusRequestEntityTooLarge)

	if got := api.listImages(owner, team.ID); len(got) != 0 {
		t.Errorf("got %d images, want none of the rejected uploads to have persisted", len(got))
	}
}

// TestUploadRosterImageEnforcesTheLimit covers the per-roster cap that bounds
// how much image data a single roster can accumulate.
func TestUploadRosterImageEnforcesTheLimit(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	for i := 0; i < maxRosterImagesPerRoster; i++ {
		api.uploadImage(owner, team.ID, fmt.Sprintf("image %d", i), pngBytes(32)).
			expect(http.StatusCreated)
	}

	resp := api.uploadImage(owner, team.ID, "one too many", pngBytes(32)).
		expect(http.StatusConflict)
	if msg := resp.errorMessage(); !strings.Contains(msg, "image limit reached") {
		t.Errorf("error = %q, want it to explain the limit", msg)
	}

	// The cap is per roster, so a second roster starts with a clean allowance.
	second := api.createRoster(owner, team.ID, "Progression", 0)
	body, contentType := imageUploadBody(t, "image", "on the other roster", pngBytes(32))
	api.doRaw(http.MethodPost, fmt.Sprintf("/api/teams/%d/images?roster_id=%d", team.ID, second.ID),
		owner.token, contentType, body).
		expect(http.StatusCreated)
}

// TestUpdateRosterImageCaption covers the caption edit and its rejections.
func TestUpdateRosterImageCaption(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	var created models.RosterImage
	api.uploadImage(owner, team.ID, "Before", pngBytes(32)).
		expect(http.StatusCreated).
		decode(&created)

	base := fmt.Sprintf("/api/teams/%d/images", team.ID)
	item := fmt.Sprintf("%s/%d", base, created.ID)

	var updated models.RosterImage
	api.do(http.MethodPut, item, owner.token, map[string]any{"caption": "  After  "}).
		expect(http.StatusOK).
		decode(&updated)
	if updated.Caption != "After" {
		t.Errorf("caption = %q, want it trimmed to After", updated.Caption)
	}

	// Clearing the caption is allowed; it is an optional label.
	api.do(http.MethodPut, item, owner.token, map[string]any{"caption": ""}).
		expect(http.StatusOK).
		decode(&updated)
	if updated.Caption != "" {
		t.Errorf("caption = %q, want it cleared", updated.Caption)
	}

	api.do(http.MethodPut, item, owner.token,
		map[string]any{"caption": strings.Repeat("c", maxImageCaptionLen+1)}).
		expect(http.StatusBadRequest)
	api.do(http.MethodPut, item, owner.token, "not an object").expect(http.StatusBadRequest)
	api.do(http.MethodPut, base+"/abc", owner.token, map[string]any{"caption": "x"}).
		expect(http.StatusBadRequest)
	api.do(http.MethodPut, base+"/999999", owner.token, map[string]any{"caption": "x"}).
		expect(http.StatusNotFound)
}

// TestDeleteRosterImage covers removal, including the ids that never resolve.
func TestDeleteRosterImage(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	var created models.RosterImage
	api.uploadImage(owner, team.ID, "Boss 1", pngBytes(32)).
		expect(http.StatusCreated).
		decode(&created)

	base := fmt.Sprintf("/api/teams/%d/images", team.ID)
	api.do(http.MethodDelete, fmt.Sprintf("%s/%d", base, created.ID), owner.token, nil).
		expect(http.StatusNoContent)
	if got := api.listImages(owner, team.ID); len(got) != 0 {
		t.Errorf("got %d images, want the roster emptied", len(got))
	}

	// Once gone, the image resolves nowhere — including on the raw route.
	api.do(http.MethodDelete, fmt.Sprintf("%s/%d", base, created.ID), owner.token, nil).
		expect(http.StatusNotFound)
	api.do(http.MethodGet, fmt.Sprintf("%s/%d/raw", base, created.ID), owner.token, nil).
		expect(http.StatusNotFound)
	api.do(http.MethodDelete, base+"/abc", owner.token, nil).expect(http.StatusBadRequest)
}

// TestRosterImageFromAnotherTeamIs404 covers the ownership check. Image ids are
// sequential and guessable, so the handler has to confirm the image's team
// matches the path rather than trusting the id.
func TestRosterImageFromAnotherTeamIs404(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	stranger := api.registerUser("stranger")
	team := api.createTeam(owner, "Sunday Core")
	otherTeam := api.createTeam(stranger, "Someone Else")

	var theirs models.RosterImage
	api.uploadImage(stranger, otherTeam.ID, "Theirs", pngBytes(32)).
		expect(http.StatusCreated).
		decode(&theirs)

	foreign := fmt.Sprintf("/api/teams/%d/images/%d", team.ID, theirs.ID)
	api.do(http.MethodGet, foreign+"/raw", owner.token, nil).expect(http.StatusNotFound)
	api.do(http.MethodPut, foreign, owner.token, map[string]any{"caption": "Hijacked"}).
		expect(http.StatusNotFound)
	api.do(http.MethodDelete, foreign, owner.token, nil).expect(http.StatusNotFound)

	if got := api.listImages(stranger, otherTeam.ID); len(got) != 1 || got[0].Caption != "Theirs" {
		t.Errorf("their images = %+v, want them untouched", got)
	}
}

// TestRosterImagePermissions covers the read/write split: anyone on the team
// can view positioning images, but only editors and owners can change them.
func TestRosterImagePermissions(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	viewer := api.registerUser("viewer")
	outsider := api.registerUser("outsider")
	team := api.createTeam(owner, "Sunday Core")

	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/share", team.ID), owner.token,
		map[string]string{"username": viewer.user.Username, "role": models.RoleViewer}).
		expect(http.StatusOK)

	var created models.RosterImage
	api.uploadImage(owner, team.ID, "Boss 1", pngBytes(32)).
		expect(http.StatusCreated).
		decode(&created)

	base := fmt.Sprintf("/api/teams/%d/images", team.ID)
	item := fmt.Sprintf("%s/%d", base, created.ID)

	api.do(http.MethodGet, base, viewer.token, nil).expect(http.StatusOK)
	api.do(http.MethodGet, item+"/raw", viewer.token, nil).expect(http.StatusOK)

	api.uploadImage(viewer, team.ID, "Nope", pngBytes(32)).expect(http.StatusForbidden)
	api.do(http.MethodPut, item, viewer.token, map[string]any{"caption": "Nope"}).
		expect(http.StatusForbidden)
	api.do(http.MethodDelete, item, viewer.token, nil).expect(http.StatusForbidden)

	api.do(http.MethodGet, base, outsider.token, nil).expect(http.StatusNotFound)
	api.do(http.MethodGet, item+"/raw", outsider.token, nil).expect(http.StatusNotFound)

	if got := api.listImages(owner, team.ID); len(got) != 1 || got[0].Caption != "Boss 1" {
		t.Errorf("images = %+v, want the viewer's attempts to have changed nothing", got)
	}
}

// TestRosterImageEndpointsRejectAnonymousCallers walks the image routes without
// a token. The raw route matters most: it serves bytes directly, so it must not
// be reachable as an unauthenticated <img src>.
func TestRosterImageEndpointsRejectAnonymousCallers(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")

	var created models.RosterImage
	api.uploadImage(owner, team.ID, "Boss 1", pngBytes(32)).
		expect(http.StatusCreated).
		decode(&created)

	base := fmt.Sprintf("/api/teams/%d/images", team.ID)
	item := fmt.Sprintf("%s/%d", base, created.ID)

	routes := []struct{ method, path string }{
		{http.MethodGet, base},
		{http.MethodPost, base},
		{http.MethodGet, item + "/raw"},
		{http.MethodPut, item},
		{http.MethodDelete, item},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			api.do(route.method, route.path, "", nil).expect(http.StatusUnauthorized)
		})
	}
}
