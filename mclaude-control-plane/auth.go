package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"golang.org/x/crypto/bcrypt"
)

// LoginRequest is the body for POST /auth/login.
type LoginRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	NKeyPublic string `json:"nkey_public,omitempty"` // ADR-0054: client-generated NKey public key
}

// LoginHostEntry is a host in the login response hosts[] array.
type LoginHostEntry struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Type string `json:"type"`
	Role string `json:"role"`
}

// LoginProjectEntry is a project in the login response projects[] array.
type LoginProjectEntry struct {
	ID       string  `json:"id"`
	Slug     string  `json:"slug"`
	Name     string  `json:"name"`
	HostSlug string  `json:"hostSlug,omitempty"`
	Status   string  `json:"status"`
}

// LoginResponse is returned on successful login.
type LoginResponse struct {
	// NATSUrl is the WebSocket URL the client should connect to for NATS.
	// Empty string means the client should derive it from its own origin.
	NATSUrl string `json:"natsUrl,omitempty"`
	// JWT is the NATS user JWT signed by the account key.
	JWT string `json:"jwt"`
	// NKeySeed is the user's NKey seed. Returned only in legacy mode (when
	// the client did not provide nkey_public in the login request).
	// Per ADR-0054, new clients generate their own NKey pairs and do not
	// receive seeds from the server.
	NKeySeed string `json:"nkeySeed,omitempty"`
	// UserID is the authenticated user's UUID.
	UserID string `json:"userId"`
	// UserSlug is the authenticated user's URL-safe slug (ADR-0046).
	// The SPA uses this as the KV key prefix for mclaude-hosts, mclaude-projects, etc.
	UserSlug string `json:"userSlug,omitempty"`
	// ExpiresAt is the Unix timestamp when the JWT expires.
	ExpiresAt int64 `json:"expiresAt"`
	// KNOWN-17: Hosts and Projects arrays.
	Hosts    []LoginHostEntry    `json:"hosts"`
	Projects []LoginProjectEntry `json:"projects"`
}

// Server holds application-wide dependencies.
// Per ADR-0035: zero K8s client. Project provisioning is delegated to
// controllers via NATS request/reply.
type Server struct {
	db          *DB
	accountKP   nkeys.KeyPair
	natsURL     string // internal broker URL (used by session-agent, not returned to browser clients)
	natsWsURL   string // external WebSocket URL returned to browser clients on login; empty = client derives from origin
	externalURL string // externally-accessible base URL for device-code verification links
	jwtExpiry   time.Duration
	adminToken  string          // break-glass admin bearer token
	providers   *providerRegistry // OAuth provider config and state store; nil when no providers configured
	nc          *nats.Conn      // NATS connection for KV writes from HTTP handlers; nil when NATS unavailable
	hostsKV     nats.KeyValue   // mclaude-hosts KV bucket; nil until StartProjectsSubscriber sets it (ADR-0046)
	s3          *s3Config       // S3-compatible storage config; nil when S3 not configured (ADR-0053)

	// ADR-0054: JWT revocation support.
	// operatorSeed is used to re-sign the account JWT when adding revocation entries.
	// sysAccountSeed is used to authenticate $SYS.REQ.CLAIMS.UPDATE publishes.
	operatorSeed    string
	sysAccountSeed  string
	accountJWTCache string // current account JWT (decoded/modified/re-signed for revocation)
}

// NewServer constructs a Server. accountKP must be an account-level NKey pair —
// it signs per-user JWTs. natsWsURL is the WebSocket URL returned to browser clients
// on login; if empty the client derives it from window.location.origin.
func NewServer(db *DB, accountKP nkeys.KeyPair, natsURL, natsWsURL string, jwtExpiry time.Duration, adminToken string) *Server {
	return &Server{
		db:         db,
		accountKP:  accountKP,
		natsURL:    natsURL,
		natsWsURL:  natsWsURL,
		jwtExpiry:  jwtExpiry,
		adminToken: adminToken,
	}
}

// SetExternalURL sets the externally-accessible base URL used in device-code verification links.
func (s *Server) SetExternalURL(url string) {
	s.externalURL = url
}

// SetRevocationCredentials loads operator and system account seeds for JWT revocation.
func (s *Server) SetRevocationCredentials(operatorSeed, sysAccountSeed, accountJWT string) {
	s.operatorSeed = operatorSeed
	s.sysAccountSeed = sysAccountSeed
	s.accountJWTCache = accountJWT
}

// SetNATSConn attaches the NATS connection to the server after startup.
// Called after NATS connects so HTTP handlers can write to KV buckets.
func (s *Server) SetNATSConn(nc *nats.Conn) {
	s.nc = nc
}

// handleLogin handles POST /auth/login.
// Validates email+password against Postgres, issues a NATS user JWT, and
// returns it alongside the NKey seed and NATS URL.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Email == "" || req.Password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}

	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	user, err := s.db.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	if !checkPassword(req.Password, user.PasswordHash) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	expirySecs := int64(s.jwtExpiry.Seconds())
	expiresAt := time.Now().Add(s.jwtExpiry).Unix()

	var jwtStr string
	var seed []byte

	if req.NKeyPublic != "" {
		// ADR-0054: client provided its own NKey public key.
		// Store it in the DB for future challenge-response auth.
		if s.db != nil {
			if setErr := s.db.SetUserNKeyPublic(r.Context(), user.ID, req.NKeyPublic); setErr != nil {
				// Non-fatal: log but continue (challenge-response may not work until next login)
				_ = setErr
			}
		}
		// Resolve host slugs for scoped JWT.
		hostSlugs := s.getUserHostSlugs(r.Context(), user.ID)
		var issueErr error
		jwtStr, issueErr = IssueUserJWT(req.NKeyPublic, user.ID, user.Slug, hostSlugs, s.accountKP, expirySecs)
		if issueErr != nil {
			http.Error(w, "failed to issue jwt", http.StatusInternalServerError)
			return
		}
		// No seed returned — client already has its NKey seed.

		// ADR-0054: ensure per-user JetStream resources exist on login.
		if s.nc != nil {
			if resErr := ensureUserResources(s.nc, user.Slug); resErr != nil {
				// Non-fatal: log and continue; resources are created on demand.
				_ = resErr
			}
		}
	} else {
		// ADR-0054: CP never generates NKey pairs for clients.
		// Backward compatibility: if a client does not provide nkey_public, use the
		// legacy path so old SPA/CLI versions continue to work during migration.
		// DEPRECATED: will be removed once all clients support nkey_public.
		var legacyErr error
		jwtStr, seed, legacyErr = IssueUserJWTLegacy(user.ID, user.Slug, s.accountKP, expirySecs)
		if legacyErr != nil {
			http.Error(w, "failed to issue jwt", http.StatusInternalServerError)
			return
		}
	}

	// KNOWN-17: Populate hosts[] and projects[].
	loginHosts := s.getLoginHosts(r.Context(), user.ID)
	loginProjects := s.getLoginProjects(r.Context(), user.ID)

	resp := LoginResponse{
		NATSUrl:   s.natsWsURL,
		JWT:       jwtStr,
		UserID:    user.ID,
		UserSlug:  user.Slug,
		ExpiresAt: expiresAt,
		Hosts:     loginHosts,
		Projects:  loginProjects,
	}
	if len(seed) > 0 {
		resp.NKeySeed = string(seed)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handleRefresh handles POST /auth/refresh.
// Validates the existing JWT from the Authorization header and issues a new one.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	oldJWT := bearerToken(r)
	if oldJWT == "" {
		http.Error(w, "authorization required", http.StatusUnauthorized)
		return
	}

	accountPub, err := s.accountKP.PublicKey()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	claims, err := DecodeUserJWT(oldJWT, accountPub)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid jwt: %v", err), http.StatusUnauthorized)
		return
	}

	// claims.Name is the user UUID (ADR-0046). Look up the user to get their slug.
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	user, err := s.db.GetUserByID(r.Context(), claims.Name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, "user not found", http.StatusUnauthorized)
		return
	}

	expirySecs := int64(s.jwtExpiry.Seconds())
	expiresAt := time.Now().Add(s.jwtExpiry).Unix()

	var newJWT string
	var seed []byte

	if user.NKeyPublic != nil && *user.NKeyPublic != "" {
		// ADR-0054: user has a stored NKey public key. Issue scoped JWT.
		hostSlugs := s.getUserHostSlugs(r.Context(), user.ID)
		var issueErr error
		newJWT, issueErr = IssueUserJWT(*user.NKeyPublic, user.ID, user.Slug, hostSlugs, s.accountKP, expirySecs)
		if issueErr != nil {
			http.Error(w, "failed to issue jwt", http.StatusInternalServerError)
			return
		}
	} else {
		// Legacy mode: generate NKey pair and return seed.
		var legacyErr error
		newJWT, seed, legacyErr = IssueUserJWTLegacy(user.ID, user.Slug, s.accountKP, expirySecs)
		if legacyErr != nil {
			http.Error(w, "failed to issue jwt", http.StatusInternalServerError)
			return
		}
	}

	// KNOWN-17: Populate hosts[] and projects[].
	loginHosts := s.getLoginHosts(r.Context(), user.ID)
	loginProjects := s.getLoginProjects(r.Context(), user.ID)

	resp := LoginResponse{
		NATSUrl:   s.natsWsURL,
		JWT:       newJWT,
		UserID:    user.ID,
		UserSlug:  user.Slug,
		ExpiresAt: expiresAt,
		Hosts:     loginHosts,
		Projects:  loginProjects,
	}
	if len(seed) > 0 {
		resp.NKeySeed = string(seed)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// connectedProviderEntry is one entry in the connectedProviders array on /auth/me.
type connectedProviderEntry struct {
	ConnectionID string `json:"connectionId"`
	ProviderID   string `json:"providerId"`
	ProviderType string `json:"providerType"`
	AuthType     string `json:"authType"`
	DisplayName  string `json:"displayName"`
	BaseURL      string `json:"baseUrl"`
	Username     string `json:"username"`
	ConnectedAt  string `json:"connectedAt"`
}

// handleMe handles GET /auth/me.
// Returns basic info about the authenticated user, including connected OAuth providers.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := s.db.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	// Fetch connected providers.
	var connectedProviders []connectedProviderEntry
	if s.db != nil {
		conns, err := s.db.GetOAuthConnectionsByUser(r.Context(), userID)
		if err == nil {
			for _, c := range conns {
				connectedProviders = append(connectedProviders, connectedProviderEntry{
					ConnectionID: c.ID,
					ProviderID:   c.ProviderID,
					ProviderType: c.ProviderType,
					AuthType:     c.AuthType,
					DisplayName:  c.DisplayName,
					BaseURL:      c.BaseURL,
					Username:     c.Username,
					ConnectedAt:  c.ConnectedAt.UTC().Format(time.RFC3339),
				})
			}
		}
	}
	if connectedProviders == nil {
		connectedProviders = []connectedProviderEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"userId":             user.ID,
		"email":              user.Email,
		"name":               user.Name,
		"connectedProviders": connectedProviders,
	})
}

// authMiddleware validates the NATS user JWT from the Authorization header
// and injects the userID into the request context.
// KNOWN-22: Enforces access boundaries — if the URL contains {uslug},
// the JWT subject must match. Returns 403 on mismatch.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			http.Error(w, "authorization required", http.StatusUnauthorized)
			return
		}

		accountPub, err := s.accountKP.PublicKey()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		claims, err := DecodeUserJWT(token, accountPub)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		userID := claims.Name
		ctx := contextWithUserID(r.Context(), userID)

		// KNOWN-22: Access boundary enforcement — check that URL {uslug} matches JWT user.
		// Extract uslug from /api/users/{uslug}/... paths.
		if strings.HasPrefix(r.URL.Path, "/api/users/") {
			pathAfter := strings.TrimPrefix(r.URL.Path, "/api/users/")
			urlUSlug := strings.SplitN(pathAfter, "/", 2)[0]
			if urlUSlug != "" && s.db != nil {
				user, uerr := s.db.GetUserByID(r.Context(), userID)
				if uerr == nil && user != nil && user.Slug != urlUSlug {
					// Check if user is admin — admins bypass access boundaries.
					if !user.IsAdmin {
						http.Error(w, "forbidden", http.StatusForbidden)
						return
					}
				}
				// Also inject user slug into context for downstream use.
				if user != nil {
					ctx = contextWithUserSlug(ctx, user.Slug)
				}
			}
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken extracts the token from "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}

// checkPassword compares a plaintext password against a stored hash.
// Uses bcrypt — empty hash always returns false (SSO-only accounts).
func checkPassword(password, hash string) bool {
	if hash == "" {
		return false
	}
	return bcryptCheck(password, hash)
}

// HashPassword generates a bcrypt hash of the given password.
// Cost 12 is suitable for production; lower it in tests if needed.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// bcryptCheck compares a plaintext password against its bcrypt hash.
var bcryptCheck = func(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// getUserHostSlugs returns the list of host slugs the user has access to
// (owned + granted via host_access table). Used for JWT issuance (ADR-0054).
func (s *Server) getUserHostSlugs(ctx context.Context, userID string) []string {
	if s.db == nil {
		return nil
	}
	slugs, err := s.db.GetHostAccessSlugs(ctx, userID)
	if err != nil {
		return nil
	}
	return slugs
}

// getLoginHosts returns the hosts[] array for LoginResponse (KNOWN-17).
func (s *Server) getLoginHosts(ctx context.Context, userID string) []LoginHostEntry {
	hosts := []LoginHostEntry{}
	if s.db == nil {
		return hosts
	}
	dbHosts, err := s.db.GetHostsByUser(ctx, userID)
	if err != nil {
		return hosts
	}
	for _, h := range dbHosts {
		hosts = append(hosts, LoginHostEntry{
			ID:   h.ID,
			Slug: h.Slug,
			Name: h.Name,
			Type: h.Type,
			Role: h.Role,
		})
	}
	return hosts
}

// getLoginProjects returns the projects[] array for LoginResponse (KNOWN-17).
func (s *Server) getLoginProjects(ctx context.Context, userID string) []LoginProjectEntry {
	projects := []LoginProjectEntry{}
	if s.db == nil {
		return projects
	}
	dbProjects, err := s.db.GetProjectsByUser(ctx, userID)
	if err != nil {
		return projects
	}
	for _, p := range dbProjects {
		projects = append(projects, LoginProjectEntry{
			ID:       p.ID,
			Slug:     p.Slug,
			Name:     p.Name,
			HostSlug: p.HostSlug,
			Status:   p.Status,
		})
	}
	return projects
}
