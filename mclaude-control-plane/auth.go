package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"golang.org/x/crypto/bcrypt"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LoginRequest is the body for POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is returned on successful login.
type LoginResponse struct {
	// NATSUrl is the WebSocket URL the client should connect to for NATS.
	// Empty string means the client should derive it from its own origin.
	NATSUrl string `json:"natsUrl,omitempty"`
	// JWT is the NATS user JWT scoped to mclaude.{userId}.>
	JWT string `json:"jwt"`
	// NKeySeed is the user's NKey seed. The client uses it to sign NATS
	// connection nonce challenges.
	NKeySeed string `json:"nkeySeed"`
	// UserID is the authenticated user's ID.
	UserID string `json:"userId"`
	// ExpiresAt is the Unix timestamp when the JWT expires.
	ExpiresAt int64 `json:"expiresAt"`
}

// Server holds application-wide dependencies.
type Server struct {
	db              *DB
	accountKP       nkeys.KeyPair
	natsURL         string // internal broker URL (used by session-agent, not returned to browser clients)
	natsWsURL       string // external WebSocket URL returned to browser clients on login; empty = client derives from origin
	jwtExpiry       time.Duration
	adminToken      string          // break-glass admin bearer token
	k8sProvisioner  *K8sProvisioner // nil when not running in a Kubernetes cluster
	k8sClient       client.Client   // controller-runtime client; nil when not in cluster
	controlPlaneNs  string          // K8s namespace where the control-plane runs (mclaude-system)
	helmReleaseName string          // Helm release name, used to derive namespace for MCProject CRs
	providers       *providerRegistry // OAuth provider config and state store; nil when no providers configured
	nc              *nats.Conn      // NATS connection for KV writes from HTTP handlers; nil when NATS unavailable
}

// NewServer constructs a Server. accountKP must be an account-level NKey pair —
// it signs per-user JWTs. natsWsURL is the WebSocket URL returned to browser clients
// on login; if empty the client derives it from window.location.origin.
// k8sClient is the controller-runtime client for creating MCProject CRs; nil when not in cluster.
// helmReleaseName is used to derive the control-plane namespace for MCProject CRs.
func NewServer(db *DB, accountKP nkeys.KeyPair, natsURL, natsWsURL string, jwtExpiry time.Duration, adminToken string, k8s *K8sProvisioner, k8sClient client.Client, controlPlaneNs, helmReleaseName string) *Server {
	return &Server{
		db:              db,
		accountKP:       accountKP,
		natsURL:         natsURL,
		natsWsURL:       natsWsURL,
		jwtExpiry:       jwtExpiry,
		adminToken:      adminToken,
		k8sProvisioner:  k8s,
		k8sClient:       k8sClient,
		controlPlaneNs:  controlPlaneNs,
		helmReleaseName: helmReleaseName,
	}
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

	jwt, seed, err := IssueUserJWT(user.ID, s.accountKP, expiresAt+expirySecs)
	if err != nil {
		http.Error(w, "failed to issue jwt", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LoginResponse{ //nolint:errcheck
		NATSUrl:   s.natsWsURL,
		JWT:       jwt,
		NKeySeed:  string(seed),
		UserID:    user.ID,
		ExpiresAt: expiresAt,
	})
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

	expirySecs := int64(s.jwtExpiry.Seconds())
	expiresAt := time.Now().Add(s.jwtExpiry).Unix()

	newJWT, seed, err := IssueUserJWT(claims.Name, s.accountKP, expiresAt+expirySecs)
	if err != nil {
		http.Error(w, "failed to issue jwt", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LoginResponse{ //nolint:errcheck
		NATSUrl:   s.natsURL,
		JWT:       newJWT,
		NKeySeed:  string(seed),
		UserID:    claims.Name,
		ExpiresAt: expiresAt,
	})
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

		ctx := contextWithUserID(r.Context(), claims.Name)
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
	// bcrypt comparison — import golang.org/x/crypto/bcrypt in production.
	// Stubbed here; fleshed out in auth category implementation.
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
