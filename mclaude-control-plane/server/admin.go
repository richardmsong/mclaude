package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"mclaude-control-plane/slug"
	subj "mclaude-control-plane/subjects"
)

// Admin endpoints are served on the main port (8080) behind isAdminMiddleware.
// isAdminMiddleware validates the caller's JWT via the standard authMiddleware,
// then checks that the authenticated user has users.is_admin = true in Postgres.
// Returns 401 if no JWT is present; 403 if JWT is valid but user is not admin.
// No static admin token — admin identity is a normal user with the is_admin flag.
//
// Routes (all under /admin/):
//
//	POST   /admin/users          — create user
//	DELETE /admin/users/{id}     — delete user
//	GET    /admin/users          — list all users
//	POST   /admin/sessions/stop  — stop a session (stub — real stop via NATS)
//
// Removed (ADR-0097): POST/GET /admin/clusters, POST /admin/clusters/{cslug}/grants,
// DELETE /admin/clusters/{cslug} — these duplicated the NATS registration path and
// used stale fields from the pre-hub-direct architecture.

// AdminUserRequest is the body for POST /admin/users.
type AdminUserRequest struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"` // optional; empty = SSO-only account
	IsAdmin  bool   `json:"isAdmin"`  // KNOWN-09: optional admin flag
}

// AdminUserResponse is a single user entry in list/create responses.
type AdminUserResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
}

// AdminSessionStopRequest is the body for POST /admin/sessions/stop.
type AdminSessionStopRequest struct {
	UserID    string `json:"userId"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
}

// handleAdminRoutes routes all /admin/* requests.
// ADR-0097: cluster endpoints removed. All host registration goes through NATS.
func (s *Server) handleAdminRoutes(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/users":
		s.adminListUsers(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/users":
		s.adminCreateUser(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/admin/users/"):
		id := strings.TrimPrefix(r.URL.Path, "/admin/users/")
		s.adminDeleteUser(w, r, id)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/sessions/stop":
		s.adminStopSession(w, r)
	// POST /admin/users/{uslug}/promote
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin/users/") && strings.HasSuffix(r.URL.Path, "/promote"):
		uslug := strings.TrimPrefix(r.URL.Path, "/admin/users/")
		uslug = strings.TrimSuffix(uslug, "/promote")
		s.adminPromoteUser(w, r, uslug)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	rows, err := s.db.Pool().Query(r.Context(),
		`SELECT id, email, name, slug FROM users ORDER BY created_at`)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []AdminUserResponse
	for rows.Next() {
		var u AdminUserResponse
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Slug); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		users = append(users, u)
	}
	if users == nil {
		users = []AdminUserResponse{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users) //nolint:errcheck
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req AdminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ID == "" || req.Email == "" || req.Name == "" {
		http.Error(w, "id, email, and name are required", http.StatusBadRequest)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var passwordHash string
	if req.Password != "" {
		var err error
		passwordHash, err = HashPassword(req.Password)
		if err != nil {
			http.Error(w, "failed to hash password", http.StatusInternalServerError)
			return
		}
	}

	user, err := s.db.CreateUser(r.Context(), req.ID, req.Email, req.Name, passwordHash)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "email already exists", http.StatusConflict)
			return
		}
		http.Error(w, "create user error", http.StatusInternalServerError)
		return
	}

	// KNOWN-09: Set admin flag if requested.
	if req.IsAdmin {
		if err := s.db.SetUserAdmin(r.Context(), user.ID, true); err != nil {
			log.Warn().Err(err).Str("userId", user.ID).Msg("set user admin failed (non-fatal)")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(AdminUserResponse{ //nolint:errcheck
		ID:    user.ID,
		Email: user.Email,
		Name:  user.Name,
		Slug:  user.Slug,
	})
}

func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request, userID string) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	if userID == "" {
		http.Error(w, "user id required", http.StatusBadRequest)
		return
	}

	// KNOWN-11 / GAP-CP-04: Before deleting the user (which cascades to hosts and
	// projects), publish delete notifications for each project on each host so
	// controllers can tear down per-project resources.
	// ADR-0097: all hosts route through user-agent unconditionally (no type branching).
	if s.nc != nil {
		user, userErr := s.db.GetUserByID(r.Context(), userID)
		if userErr == nil && user != nil {
			hosts, hostsErr := s.db.GetHostsByUser(r.Context(), userID)
			if hostsErr == nil {
				for _, h := range hosts {
					projects, projErr := s.db.GetProjectsByHostSlug(r.Context(), userID, h.Slug)
					if projErr == nil {
						for _, p := range projects {
							publishProjectsDeleteToHost(s.nc, user.Slug, h.Slug, p.Slug, p.ID)
						}
					}
				}
			}
			// Broadcast user-level projects.updated so SPA watchers refresh.
			publishProjectsUpdated(s.nc, user.Slug)
		}
	}

	// R7-G2 (ADR-0052): NATS JWT revocation on user deletion.
	//
	// TODO(R7-G2): Implement NATS JWT revocation when deleting a user.
	// Currently, the deleted user's NATS JWT remains valid until it expires
	// naturally (8h, per JWT_EXPIRY_SECONDS). This is a security gap — the
	// user can still connect to NATS and interact with their resources during
	// that window.
	//
	// Why this is not yet implemented:
	// 1. User NKey pairs are ephemeral — generated per login in IssueUserJWT()
	//    and not stored in Postgres. We cannot target a specific user's NKey
	//    public key for revocation without tracking issued keys.
	// 2. NATS account-level revocation requires modifying the account JWT's
	//    Revocations list and re-signing it with the operator key. The
	//    control-plane only has the account signing key (NATS_ACCOUNT_SEED),
	//    not the operator key needed to re-sign the account JWT.
	// 3. The hub NATS uses a MEMORY resolver with a preloaded account JWT.
	//    Even with the operator key, pushing updated claims at runtime
	//    requires the system account ($SYS.REQ.CLAIMS.UPDATE) which the
	//    control-plane does not have credentials for.
	//
	// To implement properly:
	// a) Store issued NKey public keys per user (e.g. in a nats_keys table)
	// b) Load the operator seed from OPERATOR_KEYS_PATH at startup
	// c) Decode the account JWT, add revocations for all the user's NKey
	//    public keys, re-sign with operator key
	// d) Push updated account JWT via $SYS.REQ.CLAIMS.UPDATE (requires
	//    system account credentials)
	// Alternatively, switch from MEMORY resolver to a full resolver that
	// supports runtime updates.
	//
	// Mitigation: JWT expires naturally in 8h (JWT_EXPIRY_SECONDS default).
	// The Postgres cascade-delete removes all user data, so even if the user
	// reconnects, auth middleware lookups will fail.
	log.Warn().Str("userId", userID).
		Msg("R7-G2: deleted user's NATS JWT not revoked — remains valid until natural expiry (8h)")

	if err := s.db.DeleteUser(r.Context(), userID); err != nil {
		http.Error(w, "delete error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminStopSession(w http.ResponseWriter, r *http.Request) {
	var req AdminSessionStopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == "" || req.ProjectID == "" || req.SessionID == "" {
		http.Error(w, "userId, projectId, and sessionId are required", http.StatusBadRequest)
		return
	}

	// CP-3 (ADR-0052): Publish to the correct host-scoped, project-scoped
	// sessions.delete subject that the session-agent actually subscribes to:
	// mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete
	//
	// The previous code published to mclaude.users.{uslug}.api.sessions.stop
	// which no component subscribes to — break-glass admin stop was non-functional.
	if s.nc != nil && s.db != nil {
		// Look up user slug for the NATS subject.
		var userSlug string
		if user, err := s.db.GetUserByID(r.Context(), req.UserID); err == nil && user != nil {
			userSlug = user.Slug
		}

		// Look up the project to get projectSlug and hostSlug.
		var projectSlug, hostSlug string
		if proj, err := s.db.GetProjectByID(r.Context(), req.ProjectID); err == nil && proj != nil {
			projectSlug = proj.Slug
			hostSlug = proj.HostSlug
		}

		if userSlug != "" && hostSlug != "" && projectSlug != "" {
			stopPayload, _ := json.Marshal(map[string]string{
				"sessionSlug": req.SessionID, // session-agent matches by sessionSlug or sessionId
				"sessionId":   req.SessionID,
				"requestId":   uuid.NewString(),
			})
			subject := subj.UserHostProjectSessionsDelete(
				slug.UserSlug(userSlug),
				slug.HostSlug(hostSlug),
				slug.ProjectSlug(projectSlug),
				slug.SessionSlug(req.SessionID),
			)
			if err := s.nc.Publish(subject, stopPayload); err != nil {
				log.Warn().Err(err).Str("subject", subject).Msg("admin stop session: NATS publish failed")
			} else {
				log.Info().
					Str("userSlug", userSlug).
					Str("hostSlug", hostSlug).
					Str("projectSlug", projectSlug).
					Str("sessionId", req.SessionID).
					Msg("admin stop session: published sessions.delete")
			}
		} else {
			log.Warn().
				Str("userId", req.UserID).
				Str("projectId", req.ProjectID).
				Str("userSlug", userSlug).
				Str("hostSlug", hostSlug).
				Str("projectSlug", projectSlug).
				Msg("admin stop session: missing context for NATS subject — cannot route delete")
		}
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"}) //nolint:errcheck
}

// adminPromoteUser handles POST /admin/users/{uslug}/promote (KNOWN-10).
func (s *Server) adminPromoteUser(w http.ResponseWriter, r *http.Request, uslug string) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	if uslug == "" {
		http.Error(w, "user slug required", http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserBySlug(r.Context(), uslug)
	if err != nil || user == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	if err := s.db.SetUserAdmin(r.Context(), user.ID, true); err != nil {
		http.Error(w, "failed to promote user", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "promoted"}) //nolint:errcheck
}
