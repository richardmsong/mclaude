package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"mclaude.io/common/pkg/slug"
	"mclaude.io/common/pkg/subj"
)

// ProjectResponse is the HTTP response for a single project.
type ProjectResponse struct {
	ID       string  `json:"id"`
	Slug     string  `json:"slug"`
	Name     string  `json:"name"`
	GitURL   string  `json:"gitUrl"`
	Status   string  `json:"status"`
	HostSlug string  `json:"hostSlug,omitempty"`
}

// ProjectCreateRequest is the body for POST /api/users/{uslug}/projects.
type ProjectCreateRequest struct {
	Name          string  `json:"name"`
	GitURL        string  `json:"gitUrl"`
	HostSlug      string  `json:"hostSlug,omitempty"`
	GitIdentityID *string `json:"gitIdentityId,omitempty"`
}

// handleProjectHTTPRoutes dispatches /api/users/{uslug}/projects/* requests.
// KNOWN-19: HTTP project CRUD endpoints.
func (s *Server) handleProjectHTTPRoutes(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/users/{uslug}/projects[/{pslug}]
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	parts := strings.SplitN(path, "/", 4) // [uslug, "projects", pslug?, ...]

	if len(parts) < 2 || parts[1] != "projects" {
		http.NotFound(w, r)
		return
	}

	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodGet && len(parts) == 2:
		// GET /api/users/{uslug}/projects
		s.handleListProjectsHTTP(w, r, userID)
	case r.Method == http.MethodPost && len(parts) == 2:
		// POST /api/users/{uslug}/projects
		s.handleCreateProjectHTTP(w, r, userID)
	case r.Method == http.MethodGet && len(parts) >= 3:
		// GET /api/users/{uslug}/projects/{pslug}
		s.handleGetProjectHTTP(w, r, userID, parts[2])
	case r.Method == http.MethodDelete && len(parts) >= 3:
		// DELETE /api/users/{uslug}/projects/{pslug}
		s.handleDeleteProjectHTTP(w, r, userID, parts[2])
	default:
		http.NotFound(w, r)
	}
}

// handleListProjectsHTTP handles GET /api/users/{uslug}/projects.
func (s *Server) handleListProjectsHTTP(w http.ResponseWriter, r *http.Request, userID string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	projects, err := s.db.GetProjectsByUser(r.Context(), userID)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}

	resp := make([]ProjectResponse, 0, len(projects))
	for _, p := range projects {
		resp = append(resp, ProjectResponse{
			ID:       p.ID,
			Slug:     p.Slug,
			Name:     p.Name,
			GitURL:   p.GitURL,
			Status:   p.Status,
			HostSlug: p.HostSlug,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handleCreateProjectHTTP handles POST /api/users/{uslug}/projects.
func (s *Server) handleCreateProjectHTTP(w http.ResponseWriter, r *http.Request, userID string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	var req ProjectCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	id := uuid.NewString()
	proj, err := s.db.CreateProjectWithIdentity(r.Context(), id, userID, req.Name, req.GitURL, req.GitIdentityID)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "project slug already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create project", http.StatusInternalServerError)
		return
	}

	// CP-6 (ADR-0052): After Postgres insert, perform KV write, provisioning request,
	// and publishProjectsUpdated broadcast — same as the NATS-based handler.
	// Resolve user slug for KV writes and NATS subjects.
	var userSlug string
	if user, uerr := s.db.GetUserByID(r.Context(), userID); uerr == nil && user != nil {
		userSlug = user.Slug
	}

	hostSlug := req.HostSlug
	if hostSlug == "" {
		hostSlug = proj.HostSlug // from DB join
	}

	// Write to KV so session-store watchers pick it up immediately.
	if s.nc != nil && userSlug != "" {
		if kvErr := writeProjectKV(s.nc, userID, userSlug, hostSlug, proj); kvErr != nil {
			log.Warn().Err(kvErr).Str("projectId", id).Msg("HTTP project create: write KV failed (non-fatal)")
		}
	}

	// Publish provisioning request to the host-scoped subject so the controller
	// creates per-project resources (K8s namespace/Deployment or local worktree).
	if s.nc != nil && userSlug != "" && hostSlug != "" {
		gitIdentityIDStr := ""
		if req.GitIdentityID != nil {
			gitIdentityIDStr = *req.GitIdentityID
		}
		provReq := ProvisionRequest{
			UserID:        userID,
			UserSlug:      userSlug,
			HostSlug:      hostSlug,
			ProjectID:     proj.ID,
			ProjectSlug:   proj.Slug,
			GitURL:        req.GitURL,
			GitIdentityID: gitIdentityIDStr,
		}
		provData, _ := json.Marshal(provReq)
		// ADR-0054: publish to host-scoped fan-out subject.
		provSubject := subj.HostUserProjectsCreate(slug.HostSlug(hostSlug), slug.UserSlug(userSlug), slug.ProjectSlug(proj.Slug))
		provReply, provErr := s.nc.Request(provSubject, provData, provisionTimeoutSeconds())
		if provErr != nil {
			log.Error().Err(provErr).
				Str("userSlug", userSlug).
				Str("hostSlug", hostSlug).
				Str("projectId", id).
				Msg("HTTP project create: provisioning request timed out — host unreachable")
		} else {
			var reply ProvisionReply
			if jsonErr := json.Unmarshal(provReply.Data, &reply); jsonErr == nil && !reply.OK {
				log.Error().
					Str("userSlug", userSlug).
					Str("projectId", id).
					Str("error", reply.Error).
					Msg("HTTP project create: provisioning failed")
			}
		}
	}

	// Broadcast project state change to SPA.
	if s.nc != nil && userSlug != "" {
		publishProjectsUpdated(s.nc, userSlug)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(ProjectResponse{ //nolint:errcheck
		ID:       proj.ID,
		Slug:     proj.Slug,
		Name:     proj.Name,
		GitURL:   proj.GitURL,
		Status:   proj.Status,
		HostSlug: hostSlug,
	})
}

// handleGetProjectHTTP handles GET /api/users/{uslug}/projects/{pslug}.
func (s *Server) handleGetProjectHTTP(w http.ResponseWriter, r *http.Request, userID, pslug string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	row := s.db.pool.QueryRow(r.Context(), `
		SELECT p.id, p.slug, p.name, p.git_url, p.status, COALESCE(h.slug, '')
		FROM projects p
		LEFT JOIN hosts h ON h.id = p.host_id
		WHERE p.user_id = $1 AND p.slug = $2`, userID, pslug)

	var resp ProjectResponse
	if err := row.Scan(&resp.ID, &resp.Slug, &resp.Name, &resp.GitURL, &resp.Status, &resp.HostSlug); err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handleDeleteProjectHTTP handles DELETE /api/users/{uslug}/projects/{pslug}.
// CP-04 (ADR-0052): Publishes NATS notifications before the SQL delete so
// controllers tear down K8s resources and SPA watchers see the deletion.
func (s *Server) handleDeleteProjectHTTP(w http.ResponseWriter, r *http.Request, userID, pslug string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// CP-04: Before deleting, look up the project to get its ID and host slug
	// for NATS notifications (same pattern as handleDeleteHost in hosts.go).
	var projectID, hostSlug string
	err := s.db.pool.QueryRow(r.Context(), `
		SELECT p.id, COALESCE(h.slug, '')
		FROM projects p
		LEFT JOIN hosts h ON h.id = p.host_id
		WHERE p.user_id = $1 AND p.slug = $2`, userID, pslug).Scan(&projectID, &hostSlug)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	// Resolve user slug for NATS subjects and S3 cleanup.
	var userSlug string
	if s.nc != nil || s.s3 != nil {
		if user, uerr := s.db.GetUserByID(r.Context(), userID); uerr == nil && user != nil {
			userSlug = user.Slug
		}
	}

	// CP-04: Publish delete notification to controller so it tears down
	// per-project K8s resources (namespace, Deployment, PVCs, RBAC).
	if s.nc != nil && userSlug != "" && hostSlug != "" {
		publishProjectsDeleteToHost(s.nc, userSlug, hostSlug, pslug, projectID)
	}

	tag, err := s.db.pool.Exec(r.Context(),
		`DELETE FROM projects WHERE user_id = $1 AND slug = $2`, userID, pslug)
	if err != nil {
		http.Error(w, "delete error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	// ADR-0053: Delete S3 prefix for this project (best-effort).
	// Removes all import archives and attachments under {uslug}/{hslug}/{pslug}/.
	if s.s3 != nil && userSlug != "" && hostSlug != "" {
		prefix := userSlug + "/" + hostSlug + "/" + pslug + "/"
		if s3Err := s.s3.s3DeletePrefix(prefix); s3Err != nil {
			log.Warn().Err(s3Err).Str("prefix", prefix).Msg("project delete: S3 cleanup failed (non-fatal)")
		}
	}

	// CP-04: Broadcast projects.updated so SPA watchers refresh.
	if s.nc != nil && userSlug != "" {
		publishProjectsUpdated(s.nc, userSlug)
	}

	w.WriteHeader(http.StatusNoContent)
}
