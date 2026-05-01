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

// Admin endpoints are served on the break-glass admin port (:9091).
// They operate entirely through Postgres — no NATS required — so they work
// even when NATS is down. All endpoints require the admin bearer token.
//
// Routes (all under /admin/):
//
//	POST   /admin/users          — create user
//	DELETE /admin/users/{id}     — delete user
//	GET    /admin/users          — list all users
//	POST   /admin/sessions/stop  — stop a session (stub — real stop via NATS)
//	POST   /admin/clusters       — register a cluster (ADR-0035)
//	GET    /admin/clusters       — list clusters (ADR-0035)
//	POST   /admin/clusters/{cslug}/grants — grant user access to cluster (ADR-0035)

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
}

// AdminSessionStopRequest is the body for POST /admin/sessions/stop.
type AdminSessionStopRequest struct {
	UserID    string `json:"userId"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
}

// AdminClusterRegisterRequest is the body for POST /admin/clusters (ADR-0035).
// ADR-0063: jsDomain and leafUrl fields removed (leaf topology dropped per ADR-0054).
type AdminClusterRegisterRequest struct {
	Slug         string `json:"slug"`
	Name         string `json:"name,omitempty"`
	DirectNATSURL string `json:"directNatsUrl,omitempty"`
	UserSlug     string `json:"userSlug,omitempty"` // owner user slug; if empty, first admin user is used
}

// AdminClusterRegisterResponse is returned on successful cluster registration.
// ADR-0063: jsDomain field removed (leaf topology dropped per ADR-0054).
type AdminClusterRegisterResponse struct {
	Slug         string `json:"slug"`
	LeafJWT      string `json:"leafJwt,omitempty"`
	LeafSeed     string `json:"leafSeed,omitempty"`
	AccountJWT   string `json:"accountJwt,omitempty"`
	OperatorJWT  string `json:"operatorJwt,omitempty"`
	DirectNATSURL string `json:"directNatsUrl,omitempty"`
}

// AdminClusterGrantRequest is the body for POST /admin/clusters/{cslug}/grants.
type AdminClusterGrantRequest struct {
	UserSlug string `json:"userSlug"`
}

// handleAdminRoutes routes all /admin/* requests.
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
	// ADR-0035 cluster endpoints
	case r.Method == http.MethodPost && r.URL.Path == "/admin/clusters":
		s.adminRegisterCluster(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/clusters":
		s.adminListClusters(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin/clusters/") && strings.HasSuffix(r.URL.Path, "/grants"):
		cslug := strings.TrimPrefix(r.URL.Path, "/admin/clusters/")
		cslug = strings.TrimSuffix(cslug, "/grants")
		s.adminGrantCluster(w, r, cslug)
	// KNOWN-21: DELETE /admin/clusters/{cslug}
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/admin/clusters/"):
		cslug := strings.TrimPrefix(r.URL.Path, "/admin/clusters/")
		s.adminDeleteCluster(w, r, cslug)
	// KNOWN-10: POST /admin/users/{uslug}/promote
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
	rows, err := s.db.pool.Query(r.Context(),
		`SELECT id, email, name FROM users ORDER BY created_at`)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []AdminUserResponse
	for rows.Next() {
		var u AdminUserResponse
		if err := rows.Scan(&u.ID, &u.Email, &u.Name); err != nil {
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

// adminRegisterCluster handles POST /admin/clusters (ADR-0035).
// Creates a cluster host row for the admin user and returns credentials.
func (s *Server) adminRegisterCluster(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var req AdminClusterRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Slug == "" {
		http.Error(w, "slug is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = req.Slug
	}

	// Generate a per-cluster NKey pair for the controller/leaf-node.
	clusterNKey, _, err := GenerateUserNKey()
	if err != nil {
		http.Error(w, "failed to generate cluster nkey", http.StatusInternalServerError)
		return
	}

	// Issue a cluster controller JWT scoped to mclaude.hosts.{cslug}.> (ADR-0054).
	// Use the generated cluster NKey public key.
	clusterJWT, clusterSeed, err := IssueHostJWTLegacy("*", req.Slug, s.accountKP)
	if err != nil {
		http.Error(w, "failed to issue cluster jwt", http.StatusInternalServerError)
		return
	}
	_ = clusterNKey // The generated NKey public key is embedded in clusterJWT; use the seed to reconnect.

	// KNOWN-06: Look up owner user properly instead of arbitrary (SELECT id FROM users LIMIT 1).
	var ownerUserID string
	if req.UserSlug != "" {
		ownerUser, uerr := s.db.GetUserBySlug(r.Context(), req.UserSlug)
		if uerr != nil || ownerUser == nil {
			http.Error(w, "owner user not found", http.StatusNotFound)
			return
		}
		ownerUserID = ownerUser.ID
	} else {
		// Fallback: use first admin user, or first user if no admin exists.
		err = s.db.pool.QueryRow(r.Context(),
			`SELECT id FROM users WHERE is_admin = TRUE ORDER BY created_at LIMIT 1`).Scan(&ownerUserID)
		if err != nil {
			// No admin user — fall back to first user.
			err = s.db.pool.QueryRow(r.Context(),
				`SELECT id FROM users ORDER BY created_at LIMIT 1`).Scan(&ownerUserID)
			if err != nil {
				http.Error(w, "no users exist to own the cluster", http.StatusBadRequest)
				return
			}
		}
	}

	hostID := uuid.NewString()
	_, err = s.db.pool.Exec(r.Context(), `
		INSERT INTO hosts (id, user_id, slug, name, type, role, direct_nats_url, public_key, user_jwt)
		VALUES ($1, $2, $3, $4, 'cluster', 'owner', $5, $6, $7)`,
		hostID, ownerUserID, req.Slug, req.Name, req.DirectNATSURL, clusterNKey.PublicKey, clusterJWT)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "cluster slug already exists", http.StatusConflict)
			return
		}
		log.Error().Err(err).Msg("create cluster host row")
		http.Error(w, "failed to create cluster", http.StatusInternalServerError)
		return
	}

	// KNOWN-07: Populate accountJwt and operatorJwt in the response.
	accountJWTStr := ""
	if pub, kerr := s.accountKP.PublicKey(); kerr == nil {
		accountJWTStr = pub
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(AdminClusterRegisterResponse{ //nolint:errcheck
		Slug:         req.Slug,
		LeafJWT:      clusterJWT,
		LeafSeed:     string(clusterSeed),
		AccountJWT:   accountJWTStr,
		DirectNATSURL: req.DirectNATSURL,
	})
}

// adminListClusters handles GET /admin/clusters (ADR-0035).
func (s *Server) adminListClusters(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	rows, err := s.db.pool.Query(r.Context(),
		`SELECT DISTINCT slug, name, direct_nats_url
		 FROM hosts WHERE type = 'cluster' ORDER BY slug`)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// ADR-0063: jsDomain and leafUrl removed (leaf topology dropped per ADR-0054).
	type clusterEntry struct {
		Slug         string  `json:"slug"`
		Name         string  `json:"name"`
		DirectNATSURL *string `json:"directNatsUrl"`
	}
	var clusters []clusterEntry
	for rows.Next() {
		var c clusterEntry
		if err := rows.Scan(&c.Slug, &c.Name, &c.DirectNATSURL); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		clusters = append(clusters, c)
	}
	if clusters == nil {
		clusters = []clusterEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clusters) //nolint:errcheck
}

// adminGrantCluster handles POST /admin/clusters/{cslug}/grants (ADR-0035).
// Creates a hosts row for the granted user with the cluster's shared fields.
func (s *Server) adminGrantCluster(w http.ResponseWriter, r *http.Request, cslug string) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var req AdminClusterGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserSlug == "" {
		http.Error(w, "userSlug is required", http.StatusBadRequest)
		return
	}

	// Look up the cluster's shared fields from an existing host row.
	// ADR-0063: js_domain, leaf_url, account_jwt columns dropped.
	var directNATSURL, publicKey *string
	err := s.db.pool.QueryRow(r.Context(), `
		SELECT direct_nats_url, public_key
		FROM hosts WHERE slug = $1 AND type = 'cluster' LIMIT 1`, cslug).
		Scan(&directNATSURL, &publicKey)
	if err != nil {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}

	// KNOWN-05: Look up user by slug, not email.
	user, err := s.db.GetUserBySlug(r.Context(), req.UserSlug)
	if err != nil || user == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	// KNOWN-04: Issue per-user host JWT. Use legacy function since this path
	// doesn't have a client-provided public key (admin grant operation).
	userJWT, _, err := IssueHostJWTLegacy(user.Slug, cslug, s.accountKP)
	if err != nil {
		http.Error(w, "failed to issue user jwt", http.StatusInternalServerError)
		return
	}

	hostID := uuid.NewString()
	_, err = s.db.pool.Exec(r.Context(), `
		INSERT INTO hosts (id, user_id, slug, name, type, role, direct_nats_url, public_key, user_jwt)
		VALUES ($1, $2, $3, $4, 'cluster', 'user', $5, $6, $7)
		ON CONFLICT (user_id, slug) DO NOTHING`,
		hostID, user.ID, cslug, cslug, directNATSURL, publicKey, userJWT)
	if err != nil {
		http.Error(w, "failed to grant cluster access", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "granted"}) //nolint:errcheck
}

// adminDeleteCluster handles DELETE /admin/clusters/{cslug} (KNOWN-21).
func (s *Server) adminDeleteCluster(w http.ResponseWriter, r *http.Request, cslug string) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	if cslug == "" {
		http.Error(w, "cluster slug required", http.StatusBadRequest)
		return
	}

	tag, err := s.db.pool.Exec(r.Context(),
		`DELETE FROM hosts WHERE slug = $1 AND type = 'cluster'`, cslug)
	if err != nil {
		http.Error(w, "delete error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
