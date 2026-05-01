package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

// AttachmentUploadURLRequest is the body for POST /api/attachments/upload-url (ADR-0053).
type AttachmentUploadURLRequest struct {
	Filename    string `json:"filename"`
	MimeType    string `json:"mimeType"`
	SizeBytes   int64  `json:"sizeBytes"`
	ProjectSlug string `json:"projectSlug"`
	HostSlug    string `json:"hostSlug"`
}

// AttachmentUploadURLResponse is returned for POST /api/attachments/upload-url.
type AttachmentUploadURLResponse struct {
	ID        string `json:"id"`
	UploadURL string `json:"uploadUrl"`
}

// AttachmentDownloadResponse is returned for GET /api/attachments/{id}.
type AttachmentDownloadResponse struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	MimeType    string `json:"mimeType"`
	SizeBytes   int64  `json:"sizeBytes"`
	DownloadURL string `json:"downloadUrl"`
}

// handleAttachmentRoutes dispatches /api/attachments/* requests.
func (s *Server) handleAttachmentRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/attachments")

	// POST /api/attachments/upload-url
	if r.Method == http.MethodPost && path == "/upload-url" {
		s.handleAttachmentUploadURL(w, r)
		return
	}

	// POST /api/attachments/{id}/confirm
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/confirm") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/confirm")
		s.handleAttachmentConfirm(w, r, id)
		return
	}

	// GET /api/attachments/{id}
	if r.Method == http.MethodGet && len(path) > 1 {
		id := strings.TrimPrefix(path, "/")
		s.handleAttachmentGet(w, r, id)
		return
	}

	http.NotFound(w, r)
}

// handleAttachmentUploadURL handles POST /api/attachments/upload-url (ADR-0053).
// CP validates user owns the project, generates S3 key, creates an attachments row,
// signs an upload URL (5-min TTL), returns {id, uploadUrl}.
func (s *Server) handleAttachmentUploadURL(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.s3 == nil {
		http.Error(w, "S3 not configured", http.StatusServiceUnavailable)
		return
	}

	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req AttachmentUploadURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Filename == "" || req.MimeType == "" || req.SizeBytes <= 0 || req.ProjectSlug == "" || req.HostSlug == "" {
		http.Error(w, "filename, mimeType, sizeBytes, projectSlug, and hostSlug are required", http.StatusBadRequest)
		return
	}
	if req.SizeBytes > maxAttachmentBytes {
		http.Error(w, fmt.Sprintf("sizeBytes exceeds limit of %d bytes", maxAttachmentBytes), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Validate user owns the project.
	user, err := s.db.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	// Look up the project.
	proj, err := s.db.GetProjectByUserAndSlug(ctx, userID, req.ProjectSlug)
	if err != nil || proj == nil {
		http.Error(w, "project not found or unauthorized", http.StatusNotFound)
		return
	}
	if proj.HostSlug != req.HostSlug {
		http.Error(w, "project is not on the specified host", http.StatusBadRequest)
		return
	}

	// Look up the host by slug (need host ID for attachments table FK).
	host, err := s.db.GetHostBySlug(ctx, req.HostSlug)
	if err != nil || host == nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}

	// Generate attachment ID and S3 key.
	attID := "att-" + uuid.NewString()[:8]
	s3Key := user.Slug + "/" + req.HostSlug + "/" + req.ProjectSlug + "/attachments/" + attID

	// Create attachment record with confirmed=false.
	att := &Attachment{
		ID:        attID,
		S3Key:     s3Key,
		Filename:  req.Filename,
		MimeType:  req.MimeType,
		SizeBytes: req.SizeBytes,
		UserID:    userID,
		HostID:    host.ID,
		ProjectID: proj.ID,
		Confirmed: false,
	}
	if err := s.db.CreateAttachment(ctx, att); err != nil {
		log.Error().Err(err).Str("attId", attID).Msg("create attachment record")
		http.Error(w, "failed to create attachment", http.StatusInternalServerError)
		return
	}

	// Generate pre-signed PUT URL (5-min TTL).
	uploadURL, err := s.s3.presignPutURL(s3Key, 5*60)
	if err != nil {
		log.Error().Err(err).Str("s3Key", s3Key).Msg("presign attachment upload URL")
		http.Error(w, "failed to generate upload URL", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(AttachmentUploadURLResponse{ //nolint:errcheck
		ID:        attID,
		UploadURL: uploadURL,
	})
}

// handleAttachmentConfirm handles POST /api/attachments/{id}/confirm (ADR-0053).
// CP verifies the S3 object exists, sets attachments.confirmed=true.
func (s *Server) handleAttachmentConfirm(w http.ResponseWriter, r *http.Request, attID string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.s3 == nil {
		http.Error(w, "S3 not configured", http.StatusServiceUnavailable)
		return
	}

	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()

	// Look up the attachment to verify ownership and get S3 key.
	att, err := s.db.GetAttachment(ctx, attID, userID)
	if err != nil || att == nil {
		http.Error(w, "attachment not found or unauthorized", http.StatusNotFound)
		return
	}
	if att.Confirmed {
		// Already confirmed — idempotent.
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]bool{"ok": true}) //nolint:errcheck
		return
	}

	// Verify S3 object exists.
	exists, err := s.s3.s3ObjectExists(att.S3Key)
	if err != nil {
		log.Warn().Err(err).Str("s3Key", att.S3Key).Msg("check attachment S3 existence (non-fatal)")
		// Proceed anyway — S3 HEAD check is best-effort.
	}
	if !exists {
		http.Error(w, "S3 object not found — upload may not have completed", http.StatusUnprocessableEntity)
		return
	}

	// Mark as confirmed.
	if err := s.db.ConfirmAttachment(ctx, attID, userID); err != nil {
		log.Error().Err(err).Str("attId", attID).Msg("confirm attachment")
		http.Error(w, "failed to confirm attachment", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true}) //nolint:errcheck
}

// handleAttachmentGet handles GET /api/attachments/{id} (ADR-0053).
// CP validates requester owns the project, returns {id, filename, mimeType, sizeBytes, downloadUrl}.
func (s *Server) handleAttachmentGet(w http.ResponseWriter, r *http.Request, attID string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.s3 == nil {
		http.Error(w, "S3 not configured", http.StatusServiceUnavailable)
		return
	}

	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()

	att, err := s.db.GetAttachment(ctx, attID, userID)
	if err != nil || att == nil {
		http.Error(w, "attachment not found or unauthorized", http.StatusNotFound)
		return
	}
	if !att.Confirmed {
		http.Error(w, "attachment not yet confirmed", http.StatusNotFound)
		return
	}

	// Generate pre-signed GET URL (5-min TTL).
	downloadURL, err := s.s3.presignGetURL(att.S3Key, 5*60)
	if err != nil {
		log.Error().Err(err).Str("s3Key", att.S3Key).Msg("presign attachment download URL")
		http.Error(w, "failed to generate download URL", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AttachmentDownloadResponse{ //nolint:errcheck
		ID:          att.ID,
		Filename:    att.Filename,
		MimeType:    att.MimeType,
		SizeBytes:   att.SizeBytes,
		DownloadURL: downloadURL,
	})
}

// ----- NATS attachment handlers -----

// NATSAttachmentUploadRequest is the payload from session-agent for attachment upload URL.
type NATSAttachmentUploadRequest struct {
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
}

// NATSAttachmentConfirmRequest is the payload from session-agent for attachment confirmation.
type NATSAttachmentConfirmRequest struct {
	ID string `json:"id"`
}

// NATSAttachmentDownloadRequest is the payload from session-agent for download URL.
type NATSAttachmentDownloadRequest struct {
	ID string `json:"id"`
}

// handleNATSAttachmentUpload handles mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.upload
// Session-agent requests an upload URL for an attachment.
func (s *Server) handleNATSAttachmentUpload(msg *nats.Msg) {
	if s.db == nil || s.s3 == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	// Extract uslug, hslug, pslug from subject.
	uslug, hslug, pslug, err := parseImportSubject(msg.Subject)
	if err != nil {
		replyNATSError(msg, "malformed subject")
		return
	}

	var req NATSAttachmentUploadRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		replyNATSError(msg, "invalid request body")
		return
	}
	if req.Filename == "" || req.MimeType == "" || req.SizeBytes <= 0 {
		replyNATSError(msg, "filename, mimeType, sizeBytes required")
		return
	}
	if req.SizeBytes > maxAttachmentBytes {
		replyNATSError(msg, fmt.Sprintf("sizeBytes exceeds limit of %d", maxAttachmentBytes))
		return
	}

	ctx := context.Background()

	user, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || user == nil {
		replyNATSError(msg, "user not found")
		return
	}
	proj, err := s.db.GetProjectByUserAndSlug(ctx, user.ID, pslug)
	if err != nil || proj == nil || proj.HostSlug != hslug {
		replyNATSError(msg, "project not found or not on this host")
		return
	}
	host, err := s.db.GetHostBySlug(ctx, hslug)
	if err != nil || host == nil {
		replyNATSError(msg, "host not found")
		return
	}

	attID := "att-" + uuid.NewString()[:8]
	s3Key := uslug + "/" + hslug + "/" + pslug + "/attachments/" + attID

	att := &Attachment{
		ID:        attID,
		S3Key:     s3Key,
		Filename:  req.Filename,
		MimeType:  req.MimeType,
		SizeBytes: req.SizeBytes,
		UserID:    user.ID,
		HostID:    host.ID,
		ProjectID: proj.ID,
	}
	if err := s.db.CreateAttachment(ctx, att); err != nil {
		replyNATSError(msg, "failed to create attachment record")
		return
	}

	uploadURL, err := s.s3.presignPutURL(s3Key, 5*60)
	if err != nil {
		replyNATSError(msg, "failed to generate upload URL")
		return
	}

	reply, _ := json.Marshal(map[string]string{"id": attID, "uploadUrl": uploadURL})
	if msg.Reply != "" {
		_ = msg.Respond(reply)
	}
}

// handleNATSAttachmentConfirm handles mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.confirm
// Session-agent signals attachment upload is complete.
func (s *Server) handleNATSAttachmentConfirm(msg *nats.Msg) {
	if s.db == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, _, _, err := parseImportSubject(msg.Subject)
	if err != nil {
		replyNATSError(msg, "malformed subject")
		return
	}

	var req NATSAttachmentConfirmRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.ID == "" {
		replyNATSError(msg, "id is required")
		return
	}

	ctx := context.Background()

	user, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || user == nil {
		replyNATSError(msg, "user not found")
		return
	}

	if err := s.db.ConfirmAttachment(ctx, req.ID, user.ID); err != nil {
		replyNATSError(msg, "failed to confirm attachment: "+err.Error())
		return
	}

	replyNATSOK(msg)
}

// handleNATSAttachmentDownload handles mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.download
// Session-agent requests a pre-signed download URL.
func (s *Server) handleNATSAttachmentDownload(msg *nats.Msg) {
	if s.db == nil || s.s3 == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, _, _, err := parseImportSubject(msg.Subject)
	if err != nil {
		replyNATSError(msg, "malformed subject")
		return
	}

	var req NATSAttachmentDownloadRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.ID == "" {
		replyNATSError(msg, "id is required")
		return
	}

	ctx := context.Background()

	user, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || user == nil {
		replyNATSError(msg, "user not found")
		return
	}

	att, err := s.db.GetAttachment(ctx, req.ID, user.ID)
	if err != nil || att == nil {
		replyNATSError(msg, "attachment not found or unauthorized")
		return
	}

	downloadURL, err := s.s3.presignGetURL(att.S3Key, 5*60)
	if err != nil {
		replyNATSError(msg, "failed to generate download URL")
		return
	}

	reply, _ := json.Marshal(map[string]string{"downloadUrl": downloadURL})
	if msg.Reply != "" {
		_ = msg.Respond(reply)
	}
}

// ----- NATS import handlers -----

// ImportRequestPayload is the payload for import.request (ADR-0053).
type ImportRequestPayload struct {
	Slug      string `json:"slug"`
	SizeBytes int64  `json:"sizeBytes"`
}

// ImportDownloadRequest is the payload for import.download.
type ImportDownloadRequest struct {
	ImportID string `json:"importId"`
}

// ImportCompletePayload is the payload for import.complete.
type ImportCompletePayload struct {
	ID string `json:"id"`
	TS string `json:"ts"`
}

// handleNATSImportRequest handles mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.request
// CLI requests a pre-signed upload URL for a session import archive.
func (s *Server) handleNATSImportRequest(msg *nats.Msg) {
	if s.db == nil || s.s3 == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, hslug, pslug, err := parseImportSubject(msg.Subject)
	if err != nil {
		replyNATSError(msg, "malformed subject")
		return
	}

	var req ImportRequestPayload
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		replyNATSError(msg, "invalid request body")
		return
	}
	if req.SizeBytes <= 0 {
		replyNATSError(msg, "sizeBytes required")
		return
	}
	if req.SizeBytes > maxImportBytes {
		replyNATSError(msg, fmt.Sprintf("sizeBytes exceeds limit of %d", maxImportBytes))
		return
	}

	// Generate import ID and S3 key.
	importID := "imp-" + uuid.NewString()[:8]
	s3Key := uslug + "/" + hslug + "/" + pslug + "/imports/" + importID + ".tar.gz"

	uploadURL, err := s.s3.presignPutURL(s3Key, 5*60)
	if err != nil {
		replyNATSError(msg, "failed to generate upload URL")
		return
	}

	reply, _ := json.Marshal(map[string]string{"importId": importID, "uploadUrl": uploadURL})
	if msg.Reply != "" {
		_ = msg.Respond(reply)
	}
}

// handleNATSImportConfirm handles mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm
// CLI signals archive upload is complete. CP creates the project and dispatches provisioning.
func (s *Server) handleNATSImportConfirm(msg *nats.Msg) {
	if s.db == nil || s.s3 == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, hslug, pslug, err := parseImportSubject(msg.Subject)
	if err != nil {
		replyNATSError(msg, "malformed subject")
		return
	}

	var req struct {
		ImportID string `json:"importId"`
		Name     string `json:"name"` // project display name
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.ImportID == "" {
		replyNATSError(msg, "importId is required")
		return
	}

	ctx := context.Background()

	user, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || user == nil {
		replyNATSError(msg, "user not found")
		return
	}

	// Verify S3 object exists.
	s3Key := uslug + "/" + hslug + "/" + pslug + "/imports/" + req.ImportID + ".tar.gz"
	exists, err := s.s3.s3ObjectExists(s3Key)
	if err != nil {
		replyNATSError(msg, "failed to verify S3 upload: "+err.Error())
		return
	}
	if !exists {
		replyNATSError(msg, "import archive not found in S3 — upload may not have completed")
		return
	}

	// Create project with source='import' and import_ref set.
	projectName := req.Name
	if projectName == "" {
		projectName = pslug
	}
	projID := uuid.NewString()
	proj, err := s.db.CreateProjectWithIdentity(ctx, projID, user.ID, projectName, "", nil)
	if err != nil {
		replyNATSError(msg, "failed to create project")
		return
	}

	// Set import_ref on the project.
	if err := s.db.SetProjectImportRef(ctx, projID, req.ImportID); err != nil {
		log.Warn().Err(err).Str("projectId", projID).Msg("set import_ref (non-fatal)")
	}

	// Write project KV.
	if s.nc != nil {
		if kvErr := writeProjectKV(s.nc, user.ID, uslug, hslug, proj); kvErr != nil {
			log.Warn().Err(kvErr).Str("projectId", projID).Msg("import confirm: write project KV (non-fatal)")
		}
	}

	// Dispatch provisioning request to controller.
	if s.nc != nil {
		provReq := ProvisionRequest{
			UserID:    user.ID,
			UserSlug:  uslug,
			HostSlug:  hslug,
			ProjectID: projID,
			ProjectSlug: proj.Slug,
		}
		provData, _ := json.Marshal(provReq)
		provSubject := "mclaude.hosts." + hslug + ".users." + uslug + ".projects." + proj.Slug + ".create"
		if _, reqErr := s.nc.Request(provSubject, provData, 30*time.Second); reqErr != nil {
			log.Warn().Err(reqErr).Str("projectId", projID).Msg("import confirm: provisioning request (non-fatal)")
		}
	}

	reply, _ := json.Marshal(map[string]string{"projectId": projID, "projectSlug": proj.Slug})
	if msg.Reply != "" {
		_ = msg.Respond(reply)
	}
}

// handleNATSImportDownload handles mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.download
// Session-agent requests pre-signed download URL for the import archive.
func (s *Server) handleNATSImportDownload(msg *nats.Msg) {
	if s.db == nil || s.s3 == nil {
		replyNATSError(msg, "service unavailable")
		return
	}

	uslug, hslug, pslug, err := parseImportSubject(msg.Subject)
	if err != nil {
		replyNATSError(msg, "malformed subject")
		return
	}

	ctx := context.Background()

	user, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || user == nil {
		replyNATSError(msg, "user not found")
		return
	}

	// Look up the project to get its import_ref.
	proj, err := s.db.GetProjectByUserAndSlug(ctx, user.ID, pslug)
	if err != nil || proj == nil || proj.HostSlug != hslug {
		replyNATSError(msg, "project not found or not on this host")
		return
	}

	if proj.ImportRef == nil || *proj.ImportRef == "" {
		replyNATSError(msg, "project has no import archive")
		return
	}

	s3Key := uslug + "/" + hslug + "/" + pslug + "/imports/" + *proj.ImportRef + ".tar.gz"
	downloadURL, err := s.s3.presignGetURL(s3Key, 5*60)
	if err != nil {
		replyNATSError(msg, "failed to generate download URL")
		return
	}

	reply, _ := json.Marshal(map[string]string{"downloadUrl": downloadURL})
	if msg.Reply != "" {
		_ = msg.Respond(reply)
	}
}

// handleNATSImportComplete handles mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.complete
// Session-agent signals archive unpack is complete. CP deletes the S3 object.
func (s *Server) handleNATSImportComplete(msg *nats.Msg) {
	if s.db == nil || s.s3 == nil {
		// Non-fatal — just log and ignore.
		return
	}

	uslug, hslug, pslug, err := parseImportSubject(msg.Subject)
	if err != nil {
		return
	}

	var req ImportCompletePayload
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		return
	}

	ctx := context.Background()

	user, err := s.db.GetUserBySlug(ctx, uslug)
	if err != nil || user == nil {
		return
	}

	proj, err := s.db.GetProjectByUserAndSlug(ctx, user.ID, pslug)
	if err != nil || proj == nil || proj.HostSlug != hslug {
		return
	}

	if proj.ImportRef == nil || *proj.ImportRef == "" {
		return
	}

	// Delete S3 object — best-effort.
	s3Key := uslug + "/" + hslug + "/" + pslug + "/imports/" + *proj.ImportRef + ".tar.gz"
	if err := s.s3.s3DeleteObject(s3Key); err != nil {
		log.Warn().Err(err).Str("s3Key", s3Key).Msg("import complete: delete S3 archive (non-fatal)")
	}

	// Clear import_ref on the project.
	if err := s.db.SetProjectImportRef(ctx, proj.ID, ""); err != nil {
		log.Warn().Err(err).Str("projectId", proj.ID).Msg("import complete: clear import_ref (non-fatal)")
	}
}

// parseImportSubject extracts uslug, hslug, pslug from import/attachment NATS subjects.
// Pattern: mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.*
//          mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.attachments.*
// Minimum parts: mclaude(0).users(1).{uslug}(2).hosts(3).{hslug}(4).projects(5).{pslug}(6)
// = 7 tokens needed; action tokens at indices 7+ are optional for parsing.
func parseImportSubject(subject string) (uslug, hslug, pslug string, err error) {
	parts := strings.Split(subject, ".")
	// Require at least 7 parts to access indices 2, 4, 6.
	if len(parts) < 7 {
		return "", "", "", fmt.Errorf("malformed import/attachment subject: %s", subject)
	}
	return parts[2], parts[4], parts[6], nil
}
