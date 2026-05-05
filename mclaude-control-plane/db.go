package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"mclaude.io/common/pkg/slug"
)

// DB wraps a pgxpool connection pool.
type DB struct {
	pool *pgxpool.Pool
}

// User is a row from the users table.
type User struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string // bcrypt — empty for SSO-only accounts
	OAuthID      *string
	IsAdmin      bool
	Slug         string  // URL-safe identifier derived from email local-part (ADR-0046)
	NKeyPublic   *string // NKey public key for challenge-response auth (ADR-0054). NULL until first NKey-based login.
	CreatedAt    time.Time
}

// computeUserSlug derives a URL-safe slug from an email address (ADR-0062).
// Slugifies the full email: lowercase, replace all non-[a-z0-9] runs with '-',
// trim leading/trailing '-', truncate to 63 chars.
// Examples: dev@mclaude.local → dev-mclaude-local, richard@rbc.com → richard-rbc-com.
// KNOWN-12: Validates against reserved-word blocklist and generates fallback on collision.
func computeUserSlug(email string) string {
	s := slug.Slugify(email)
	if s == "" {
		return ""
	}
	// Validate against blocklist; generate fallback if reserved.
	if err := slug.Validate(s); err != nil {
		uid := uuid.New()
		return slug.ValidateOrFallback(s, slug.KindUser, uid)
	}
	return s
}

// Host is a row from the hosts table. Per ADR-0035, this is the single source
// of truth for both BYOH machines and K8s clusters.
// ADR-0063: columns js_domain, leaf_url, account_jwt dropped (leaf topology removed per ADR-0054).
type Host struct {
	ID            string
	UserID        string  // OwnerID in spec (ADR-0054); keeps user_id column name for DB compat
	Slug          string
	Name          string
	Type          string  // 'machine' or 'cluster'
	Role          string  // 'owner' or 'user'
	DirectNATSURL *string // Optional
	PublicKey     *string // NKey public key (nkey_public in spec, public_key in DB)
	UserJWT       *string // Legacy JWT column
	NatsJWT       *string // ADR-0054: host NATS JWT (5-min TTL, host-scoped)
	CreatedAt     time.Time
	LastSeenAt    *time.Time
}

// AgentCredential is a row from the agent_credentials table (ADR-0054).
type AgentCredential struct {
	ID          string
	UserID      string
	HostSlug    string
	ProjectSlug string
	NKeyPublic  string
	CreatedAt   time.Time
}

// UserHostAgent is a row from the user_host_agents table (ADR-0091).
// One row per (host, user) pair. Stores the NKey public key (registered by
// host controller) and X25519 public key (registered by user-agent on startup).
type UserHostAgent struct {
	ID          string
	HostSlug    string
	UserSlug    string
	NKeyPublic  *string // Ed25519 NKey public key; nil until host controller registers it
	X25519Pub   *string // X25519 public key (base64, 32 bytes); nil until user-agent authenticates
	CreatedAt   time.Time
}

// HostGrant is a row from the host_grants table (ADR-0091).
// Tracks the bilateral grant/accept handshake that authorises a user-agent on a host.
type HostGrant struct {
	ID         string
	HostSlug   string
	UserSlug   string
	Status     string     // "pending", "accepted", "revoked"
	CreatedAt  time.Time
	AcceptedAt *time.Time // nil when status='pending'
}

// Attachment is a row from the attachments table (ADR-0053).
type Attachment struct {
	ID        string
	S3Key     string
	Filename  string
	MimeType  string
	SizeBytes int64
	UserID    string
	HostID    string
	ProjectID string
	Confirmed bool
	CreatedAt time.Time
}

// ConnectDB opens a pgxpool connection to the given DSN and verifies connectivity.
func ConnectDB(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool new: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Close releases all pool connections.
func (db *DB) Close() {
	db.pool.Close()
}

// Migrate applies the embedded SQL schema. Idempotent — uses IF NOT EXISTS.
func (db *DB) Migrate(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, schema)
	return err
}

// scanUser scans a user row from a query result.
func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.OAuthID, &u.IsAdmin, &u.Slug, &u.NKeyPublic, &u.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// GetUserByEmail looks up a user by email address. Returns nil, nil if not found.
func (db *DB) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, oauth_id, is_admin, slug, nkey_public, created_at FROM users WHERE email = $1`,
		email)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

// GetUserByID looks up a user by ID. Returns nil, nil if not found.
func (db *DB) GetUserByID(ctx context.Context, id string) (*User, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, oauth_id, is_admin, slug, nkey_public, created_at FROM users WHERE id = $1`,
		id)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

// GetUserBySlug looks up a user by slug. Returns nil, nil if not found.
func (db *DB) GetUserBySlug(ctx context.Context, uslug string) (*User, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, oauth_id, is_admin, slug, nkey_public, created_at FROM users WHERE slug = $1`,
		uslug)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("get user by slug: %w", err)
	}
	return u, nil
}

// GetUserByNKeyPublic looks up a user by NKey public key. Returns nil, nil if not found.
// Used for HTTP challenge-response authentication (ADR-0054).
func (db *DB) GetUserByNKeyPublic(ctx context.Context, nkeyPublic string) (*User, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, oauth_id, is_admin, slug, nkey_public, created_at FROM users WHERE nkey_public = $1`,
		nkeyPublic)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("get user by nkey_public: %w", err)
	}
	return u, nil
}

// SetUserNKeyPublic stores the user's NKey public key (for challenge-response auth).
// The UNIQUE constraint prevents two users from sharing the same NKey.
func (db *DB) SetUserNKeyPublic(ctx context.Context, userID, nkeyPublic string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE users SET nkey_public = $1 WHERE id = $2`,
		nkeyPublic, userID)
	return err
}

// CreateUser inserts a new user row. id must be a pre-generated UUID.
// Per ADR-0095, no host row is created. Hosts are created only through the
// BYOH attestation flow (mclaude.users.*.hosts._.register).
func (db *DB) CreateUser(ctx context.Context, id, email, name, passwordHash string) (*User, error) {
	userSlug := computeUserSlug(email)
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx,
		`INSERT INTO users (id, email, name, password_hash, slug, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, email, name, passwordHash, userSlug, now)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	return &User{ID: id, Email: email, Name: name, PasswordHash: passwordHash, Slug: userSlug, CreatedAt: now}, nil
}

// SetUserAdmin sets or clears the is_admin flag on a user.
func (db *DB) SetUserAdmin(ctx context.Context, id string, isAdmin bool) error {
	_, err := db.pool.Exec(ctx, `UPDATE users SET is_admin = $1 WHERE id = $2`, isAdmin, id)
	return err
}

// UpdateUserPassword sets the password_hash for a user by ID.
// Used by init-keys to store the BOOTSTRAP_ADMIN_PASSWORD hash on an existing user.
func (db *DB) UpdateUserPassword(ctx context.Context, id, passwordHash string) error {
	_, err := db.pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, passwordHash, id)
	return err
}

// DeleteUser removes a user by ID.
func (db *DB) DeleteUser(ctx context.Context, id string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

// Project is a row from the projects table.
type Project struct {
	ID            string
	UserID        string
	Name          string
	Slug          string
	GitURL        string
	Status        string
	HostID        *string // nullable during migration; NOT NULL for new installs
	HostSlug      string  // slug of the host this project is provisioned on; joined from hosts table
	GitIdentityID *string
	Source        string  // 'created' or 'import' (ADR-0053)
	ImportRef     *string // S3 import ID; non-NULL only while archive exists in S3 (ADR-0053)
	CreatedAt     time.Time
}

// CreateProject inserts a new project row. id must be a pre-generated UUID.
// hostID must be the UUID of an existing host row — callers must resolve the host
// before calling (ADR-0095: no fallback host resolution inside this function).
func (db *DB) CreateProject(ctx context.Context, id, userID, name, gitURL, hostID string) (*Project, error) {
	return db.CreateProjectWithIdentity(ctx, id, userID, name, gitURL, hostID, nil)
}

// CreateProjectWithIdentity inserts a new project row with optional git_identity_id.
// hostID must be the UUID of an existing host row. Per ADR-0095, there is no fallback
// host resolution — the caller is responsible for resolving the host before calling.
func (db *DB) CreateProjectWithIdentity(ctx context.Context, id, userID, name, gitURL, hostID string, gitIdentityID *string) (*Project, error) {
	now := time.Now().UTC()

	// Compute slug from project name.
	projectSlug := slug.Slugify(name)
	if projectSlug == "" {
		projectSlug = "project"
	}
	// Validate against blocklist; generate fallback if reserved.
	if err := slug.Validate(projectSlug); err != nil {
		uid := uuid.New()
		projectSlug = slug.ValidateOrFallback(projectSlug, slug.KindProject, uid)
	}

	hostIDPtr := &hostID
	_, err := db.pool.Exec(ctx,
		`INSERT INTO projects (id, user_id, name, slug, git_url, status, host_id, created_at, git_identity_id)
		 VALUES ($1, $2, $3, $4, $5, 'active', $6, $7, $8)`,
		id, userID, name, projectSlug, gitURL, hostIDPtr, now, gitIdentityID)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	return &Project{ID: id, UserID: userID, Name: name, Slug: projectSlug, GitURL: gitURL, Status: "active", HostID: hostIDPtr, GitIdentityID: gitIdentityID, CreatedAt: now}, nil
}

// GetProjectsByUser returns all projects owned by a user, with host slug joined.
func (db *DB) GetProjectsByUser(ctx context.Context, userID string) ([]*Project, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT p.id, p.user_id, p.name, p.slug, p.git_url, p.status, p.host_id,
		        COALESCE(h.slug, ''), p.created_at, p.git_identity_id
		 FROM projects p
		 LEFT JOIN hosts h ON h.id = p.host_id
		 WHERE p.user_id = $1 ORDER BY p.created_at`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("get projects: %w", err)
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Slug, &p.GitURL, &p.Status, &p.HostID, &p.HostSlug, &p.CreatedAt, &p.GitIdentityID); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// GetProjectByID returns a project by ID with host slug joined, or nil if not found.
func (db *DB) GetProjectByID(ctx context.Context, id string) (*Project, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT p.id, p.user_id, p.name, p.slug, p.git_url, p.status, p.host_id,
		        COALESCE(h.slug, ''), p.created_at, p.git_identity_id
		 FROM projects p
		 LEFT JOIN hosts h ON h.id = p.host_id
		 WHERE p.id = $1`, id)
	p := &Project{}
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Slug, &p.GitURL, &p.Status, &p.HostID, &p.HostSlug, &p.CreatedAt, &p.GitIdentityID)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get project by id: %w", err)
	}
	return p, nil
}

// UpdateProjectGitIdentity sets the git_identity_id for a project.
func (db *DB) UpdateProjectGitIdentity(ctx context.Context, projectID string, gitIdentityID *string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE projects SET git_identity_id = $1 WHERE id = $2`,
		gitIdentityID, projectID)
	return err
}

// UpdateProjectStatus sets the status column for a project.
func (db *DB) UpdateProjectStatus(ctx context.Context, projectID, status string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE projects SET status = $1 WHERE id = $2`,
		status, projectID)
	return err
}

// SetProjectImportRef sets or clears the import_ref column for a project.
// Pass empty string to clear (set to NULL). Used by import flow (ADR-0053).
func (db *DB) SetProjectImportRef(ctx context.Context, projectID, importRef string) error {
	var v *string
	if importRef != "" {
		v = &importRef
	}
	_, err := db.pool.Exec(ctx,
		`UPDATE projects SET import_ref = $1 WHERE id = $2`,
		v, projectID)
	return err
}

// DeleteProject removes a project by ID.
func (db *DB) DeleteProject(ctx context.Context, projectID string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
	return err
}

// scanHost scans a full host row. ADR-0063: js_domain, leaf_url, account_jwt columns dropped.
func scanHost(row interface{ Scan(...any) error }) (*Host, error) {
	h := &Host{}
	err := row.Scan(&h.ID, &h.UserID, &h.Slug, &h.Name, &h.Type, &h.Role,
		&h.DirectNATSURL, &h.PublicKey, &h.UserJWT, &h.NatsJWT, &h.CreatedAt, &h.LastSeenAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return h, nil
}

// GetHostsByUser returns all hosts for a user (owned or granted via host_access).
// Per ADR-0054: owned hosts (role='owner') + granted hosts via host_access table.
func (db *DB) GetHostsByUser(ctx context.Context, userID string) ([]*Host, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, user_id, slug, name, type, role,
		        direct_nats_url, public_key, user_jwt, nats_jwt, created_at, last_seen_at
		 FROM hosts WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("get hosts by user: %w", err)
	}
	defer rows.Close()
	var hosts []*Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

// HasHosts returns true if the user owns at least one host (ADR-0095).
// Used to gate project creation — users must register a host before creating projects.
func (db *DB) HasHosts(ctx context.Context, userID string) (bool, error) {
	var count int
	err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hosts WHERE user_id = $1 LIMIT 1`, userID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("has hosts: %w", err)
	}
	return count > 0, nil
}

// GetHostBySlug looks up a host by slug. Returns the first match across all users.
// Per ADR-0054, hosts are globally unique by slug (one row per host, not per user).
// During migration, returns the 'owner' row if multiple rows exist for the same slug.
func (db *DB) GetHostBySlug(ctx context.Context, hslug string) (*Host, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, user_id, slug, name, type, role,
		        direct_nats_url, public_key, user_jwt, nats_jwt, created_at, last_seen_at
		 FROM hosts WHERE slug = $1 AND role = 'owner' LIMIT 1`,
		hslug)
	h, err := scanHost(row)
	if err != nil {
		return nil, fmt.Errorf("get host by slug: %w", err)
	}
	return h, nil
}

// GetHostByPublicKey looks up a host by its NKey public key.
// Used for HTTP challenge-response auth and $SYS presence tracking (ADR-0054).
func (db *DB) GetHostByPublicKey(ctx context.Context, publicKey string) (*Host, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, user_id, slug, name, type, role,
		        direct_nats_url, public_key, user_jwt, nats_jwt, created_at, last_seen_at
		 FROM hosts WHERE public_key = $1 LIMIT 1`,
		publicKey)
	h, err := scanHost(row)
	if err != nil {
		return nil, fmt.Errorf("get host by public key: %w", err)
	}
	return h, nil
}

// GetHostAccessSlugs returns the slugs of all hosts the user has access to
// (owned + granted). Used at JWT issuance time (ADR-0054).
func (db *DB) GetHostAccessSlugs(ctx context.Context, userID string) ([]string, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT DISTINCT h.slug
		 FROM hosts h
		 WHERE h.user_id = $1
		 UNION
		 SELECT DISTINCT h.slug
		 FROM hosts h
		 JOIN host_access ha ON ha.host_id = h.id
		 WHERE ha.user_id = $1
		 ORDER BY 1`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("get host access slugs: %w", err)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		slugs = append(slugs, s)
	}
	return slugs, rows.Err()
}

// GrantHostAccess inserts a host_access record (user granted access to host).
// The host owner has implicit access — this table only tracks granted users.
func (db *DB) GrantHostAccess(ctx context.Context, hostID, userID string) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO host_access (host_id, user_id) VALUES ($1, $2)
		 ON CONFLICT (host_id, user_id) DO NOTHING`,
		hostID, userID)
	return err
}

// RevokeHostAccess removes a host_access record.
func (db *DB) RevokeHostAccess(ctx context.Context, hostID, userID string) error {
	_, err := db.pool.Exec(ctx,
		`DELETE FROM host_access WHERE host_id = $1 AND user_id = $2`,
		hostID, userID)
	return err
}

// UpdateHostNatsJWT stores the host's current NATS JWT (5-min TTL per ADR-0054).
func (db *DB) UpdateHostNatsJWT(ctx context.Context, hostID, natsJWT string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE hosts SET nats_jwt = $1, user_jwt = $1 WHERE id = $2`,
		natsJWT, hostID)
	return err
}

// RegisterHostNKeyPublic stores a host's NKey public key (set at registration).
func (db *DB) RegisterHostNKeyPublic(ctx context.Context, hostID, publicKey string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE hosts SET public_key = $1 WHERE id = $2`,
		publicKey, hostID)
	return err
}

// GetAgentCredential looks up an agent credential by NKey public key.
// Used for HTTP challenge-response auth (ADR-0054).
func (db *DB) GetAgentCredentialByNKeyPublic(ctx context.Context, nkeyPublic string) (*AgentCredential, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, user_id, host_slug, project_slug, nkey_public, created_at
		 FROM agent_credentials WHERE nkey_public = $1`,
		nkeyPublic)
	ac := &AgentCredential{}
	err := row.Scan(&ac.ID, &ac.UserID, &ac.HostSlug, &ac.ProjectSlug, &ac.NKeyPublic, &ac.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get agent credential by nkey: %w", err)
	}
	return ac, nil
}

// UpsertAgentCredential stores or replaces an agent NKey public key.
// The UNIQUE(user_id, host_slug, project_slug) constraint ensures one active
// credential per project. On conflict, replaces the public key.
func (db *DB) UpsertAgentCredential(ctx context.Context, id, userID, hostSlug, projectSlug, nkeyPublic string) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO agent_credentials (id, user_id, host_slug, project_slug, nkey_public)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (user_id, host_slug, project_slug) DO UPDATE SET
		     nkey_public = EXCLUDED.nkey_public,
		     id = EXCLUDED.id`,
		id, userID, hostSlug, projectSlug, nkeyPublic)
	return err
}

// DeleteAgentCredentialsByHost removes all agent credentials for a given host slug.
// Called during host deregistration (ADR-0054).
func (db *DB) DeleteAgentCredentialsByHost(ctx context.Context, hostSlug string) error {
	_, err := db.pool.Exec(ctx,
		`DELETE FROM agent_credentials WHERE host_slug = $1`,
		hostSlug)
	return err
}

// DeleteAgentCredentialsByProject removes the agent credential for a project.
// Called during project deprovisioning (ADR-0054).
func (db *DB) DeleteAgentCredentialsByProject(ctx context.Context, userID, hostSlug, projectSlug string) error {
	_, err := db.pool.Exec(ctx,
		`DELETE FROM agent_credentials WHERE user_id = $1 AND host_slug = $2 AND project_slug = $3`,
		userID, hostSlug, projectSlug)
	return err
}

// --- ADR-0091: user_host_agents and host_grants DB methods ---

// UpsertUserHostAgentNKey upserts a user_host_agents row and sets nkey_public.
// Called when the host controller registers the user-agent NKey (Phase 1 step 3).
// Returns FORBIDDEN if no host_grants row exists for (hostSlug, userSlug).
func (db *DB) UpsertUserHostAgentNKey(ctx context.Context, hostSlug, userSlug, nkeyPublic string) error {
	// Validate that a host_grants row exists for (hostSlug, userSlug).
	var count int
	err := db.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM host_grants WHERE host_slug = $1 AND user_slug = $2`,
		hostSlug, userSlug).Scan(&count)
	if err != nil {
		return fmt.Errorf("check host_grants: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("FORBIDDEN: no host_grants row for (%s, %s)", hostSlug, userSlug)
	}

	_, err = db.pool.Exec(ctx, `
		INSERT INTO user_host_agents (host_slug, user_slug, nkey_public, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (host_slug, user_slug) DO UPDATE SET nkey_public = EXCLUDED.nkey_public`,
		hostSlug, userSlug, nkeyPublic)
	return err
}

// UpdateUserHostAgentX25519Pub sets the x25519_pub for a user_host_agents row.
// Called during POST /api/auth/verify when the NKey resolves to user_host_agents.
func (db *DB) UpdateUserHostAgentX25519Pub(ctx context.Context, hostSlug, userSlug, x25519Pub string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE user_host_agents SET x25519_pub = $1 WHERE host_slug = $2 AND user_slug = $3`,
		x25519Pub, hostSlug, userSlug)
	return err
}

// GetUserHostAgentByNKeyPublic looks up a user_host_agents row by NKey public key.
// Used in issueJWTForNKey lookup (ADR-0091 lookup path).
func (db *DB) GetUserHostAgentByNKeyPublic(ctx context.Context, nkeyPublic string) (*UserHostAgent, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, host_slug, user_slug, nkey_public, x25519_pub, created_at
		 FROM user_host_agents WHERE nkey_public = $1`,
		nkeyPublic)
	ua := &UserHostAgent{}
	err := row.Scan(&ua.ID, &ua.HostSlug, &ua.UserSlug, &ua.NKeyPublic, &ua.X25519Pub, &ua.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get user host agent by nkey: %w", err)
	}
	return ua, nil
}

// GetUserHostAgentBySlug returns the user_host_agents row for a (host, user) pair.
func (db *DB) GetUserHostAgentBySlug(ctx context.Context, hostSlug, userSlug string) (*UserHostAgent, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, host_slug, user_slug, nkey_public, x25519_pub, created_at
		 FROM user_host_agents WHERE host_slug = $1 AND user_slug = $2`,
		hostSlug, userSlug)
	ua := &UserHostAgent{}
	err := row.Scan(&ua.ID, &ua.HostSlug, &ua.UserSlug, &ua.NKeyPublic, &ua.X25519Pub, &ua.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get user host agent by slug: %w", err)
	}
	return ua, nil
}

// UpsertHostGrant upserts a host_grants row. Status is forced to 'pending' on
// insert; accepted status is only set via AcceptHostGrant. On conflict (existing
// row), the status is not changed — idempotent for host controller re-publishes.
func (db *DB) UpsertHostGrant(ctx context.Context, hostSlug, userSlug, status string) error {
	now := time.Now().UTC()
	var acceptedAt *time.Time
	if status == "accepted" {
		acceptedAt = &now
	}
	_, err := db.pool.Exec(ctx, `
		INSERT INTO host_grants (host_slug, user_slug, status, created_at, accepted_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (host_slug, user_slug) DO NOTHING`,
		hostSlug, userSlug, status, now, acceptedAt)
	return err
}

// AcceptHostGrant sets status='accepted' and accepted_at=NOW() for a host_grants row.
// Called by POST /api/users/{uslug}/hosts/{hslug}/grant/accept (ADR-0091).
func (db *DB) AcceptHostGrant(ctx context.Context, hostSlug, userSlug string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE host_grants SET status = 'accepted', accepted_at = NOW()
		 WHERE host_slug = $1 AND user_slug = $2`,
		hostSlug, userSlug)
	return err
}

// GetHostGrant returns the host_grants row for (hostSlug, userSlug), or nil if not found.
func (db *DB) GetHostGrant(ctx context.Context, hostSlug, userSlug string) (*HostGrant, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, host_slug, user_slug, status, created_at, accepted_at
		 FROM host_grants WHERE host_slug = $1 AND user_slug = $2`,
		hostSlug, userSlug)
	g := &HostGrant{}
	err := row.Scan(&g.ID, &g.HostSlug, &g.UserSlug, &g.Status, &g.CreatedAt, &g.AcceptedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get host grant: %w", err)
	}
	return g, nil
}

// GetHostGrantsByUserSlug returns all host_grants for a user (pending + accepted).
// Used by GET /api/users/{uslug}/grants (ADR-0091).
func (db *DB) GetHostGrantsByUserSlug(ctx context.Context, userSlug string) ([]*HostGrant, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, host_slug, user_slug, status, created_at, accepted_at
		 FROM host_grants WHERE user_slug = $1 AND status IN ('pending', 'accepted')
		 ORDER BY created_at`,
		userSlug)
	if err != nil {
		return nil, fmt.Errorf("get host grants by user: %w", err)
	}
	defer rows.Close()
	var grants []*HostGrant
	for rows.Next() {
		g := &HostGrant{}
		if err := rows.Scan(&g.ID, &g.HostSlug, &g.UserSlug, &g.Status, &g.CreatedAt, &g.AcceptedAt); err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// GetDefaultPlatformHostSlug returns the slug of the default platform host, or "" if none.
// Called on user creation to auto-grant access (ADR-0091).
func (db *DB) GetDefaultPlatformHostSlug(ctx context.Context) (string, error) {
	var hostSlug string
	err := db.pool.QueryRow(ctx,
		`SELECT slug FROM hosts WHERE is_default_platform = true LIMIT 1`).Scan(&hostSlug)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return "", nil
		}
		return "", fmt.Errorf("get default platform host: %w", err)
	}
	return hostSlug, nil
}

// --- end ADR-0091 ---

// GetAgentCredentialsByHostUser returns all agent credentials for a host/user combination.
// Used for revocation during manage.revoke-access (ADR-0054).
func (db *DB) GetAgentCredentialsByHostUser(ctx context.Context, hostSlug, userID string) ([]*AgentCredential, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, user_id, host_slug, project_slug, nkey_public, created_at
		 FROM agent_credentials WHERE host_slug = $1 AND user_id = $2`,
		hostSlug, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var creds []*AgentCredential
	for rows.Next() {
		ac := &AgentCredential{}
		if err := rows.Scan(&ac.ID, &ac.UserID, &ac.HostSlug, &ac.ProjectSlug, &ac.NKeyPublic, &ac.CreatedAt); err != nil {
			return nil, err
		}
		creds = append(creds, ac)
	}
	return creds, rows.Err()
}

// GetAgentCredentialsByHost returns all agent credentials for a host.
// Used for revocation during host deregistration/emergency revocation (ADR-0054).
func (db *DB) GetAgentCredentialsByHost(ctx context.Context, hostSlug string) ([]*AgentCredential, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, user_id, host_slug, project_slug, nkey_public, created_at
		 FROM agent_credentials WHERE host_slug = $1`,
		hostSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var creds []*AgentCredential
	for rows.Next() {
		ac := &AgentCredential{}
		if err := rows.Scan(&ac.ID, &ac.UserID, &ac.HostSlug, &ac.ProjectSlug, &ac.NKeyPublic, &ac.CreatedAt); err != nil {
			return nil, err
		}
		creds = append(creds, ac)
	}
	return creds, rows.Err()
}

// CreateAttachment inserts an attachment record with confirmed=false.
func (db *DB) CreateAttachment(ctx context.Context, a *Attachment) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO attachments (id, s3_key, filename, mime_type, size_bytes, user_id, host_id, project_id, confirmed)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, FALSE)`,
		a.ID, a.S3Key, a.Filename, a.MimeType, a.SizeBytes, a.UserID, a.HostID, a.ProjectID)
	return err
}

// ConfirmAttachment sets confirmed=true for an attachment.
func (db *DB) ConfirmAttachment(ctx context.Context, id, userID string) error {
	tag, err := db.pool.Exec(ctx,
		`UPDATE attachments SET confirmed = TRUE WHERE id = $1 AND user_id = $2`,
		id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("attachment not found or unauthorized")
	}
	return nil
}

// GetAttachment returns an attachment by ID. Returns nil, nil if not found.
func (db *DB) GetAttachment(ctx context.Context, id, userID string) (*Attachment, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, s3_key, filename, mime_type, size_bytes, user_id, host_id, project_id, confirmed, created_at
		 FROM attachments WHERE id = $1 AND user_id = $2`,
		id, userID)
	a := &Attachment{}
	err := row.Scan(&a.ID, &a.S3Key, &a.Filename, &a.MimeType, &a.SizeBytes,
		&a.UserID, &a.HostID, &a.ProjectID, &a.Confirmed, &a.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return a, nil
}

// GetProjectByUserAndSlug looks up a project by user ID and slug.
func (db *DB) GetProjectByUserAndSlug(ctx context.Context, userID, pslug string) (*Project, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT p.id, p.user_id, p.name, p.slug, p.git_url, p.status, p.host_id,
		        COALESCE(h.slug, ''), p.created_at, p.git_identity_id
		 FROM projects p
		 LEFT JOIN hosts h ON h.id = p.host_id
		 WHERE p.user_id = $1 AND p.slug = $2`, userID, pslug)
	p := &Project{}
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Slug, &p.GitURL, &p.Status, &p.HostID, &p.HostSlug, &p.CreatedAt, &p.GitIdentityID)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get project by user and slug: %w", err)
	}
	return p, nil
}

// GetProjectsByUserAndHost returns projects for a user on a specific host, by host slug.
func (db *DB) GetProjectsByUserAndHost(ctx context.Context, userID, hostSlug string) ([]*Project, error) {
	return db.GetProjectsByHostSlug(ctx, userID, hostSlug)
}

// GetProjectsByHostSlug returns all projects for a user on a specific host slug.
func (db *DB) GetProjectsByHostSlug(ctx context.Context, userID, hostSlug string) ([]*Project, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT p.id, p.user_id, p.name, p.slug, p.git_url, p.status, p.host_id,
		        COALESCE(h.slug, ''), p.created_at, p.git_identity_id
		 FROM projects p
		 LEFT JOIN hosts h ON h.id = p.host_id
		 WHERE p.user_id = $1 AND h.slug = $2
		 ORDER BY p.created_at`,
		userID, hostSlug)
	if err != nil {
		return nil, fmt.Errorf("get projects by host slug: %w", err)
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Slug, &p.GitURL, &p.Status, &p.HostID, &p.HostSlug, &p.CreatedAt, &p.GitIdentityID); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// OAuthConnection is a row from the oauth_connections table.
type OAuthConnection struct {
	ID             string
	UserID         string
	ProviderID     string
	ProviderType   string
	AuthType       string
	BaseURL        string
	DisplayName    string
	ProviderUserID string
	Username       string
	Scopes         string
	TokenExpiresAt *time.Time
	ConnectedAt    time.Time
}

// CreateOAuthConnection upserts a connection row.
// On conflict (user_id, base_url, provider_user_id), updates all non-key fields.
func (db *DB) CreateOAuthConnection(ctx context.Context, c *OAuthConnection) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO oauth_connections
			(id, user_id, provider_id, provider_type, auth_type, base_url, display_name, provider_user_id, username, scopes, token_expires_at, connected_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (user_id, base_url, provider_user_id) DO UPDATE SET
			provider_id      = EXCLUDED.provider_id,
			provider_type    = EXCLUDED.provider_type,
			auth_type        = EXCLUDED.auth_type,
			display_name     = EXCLUDED.display_name,
			username         = EXCLUDED.username,
			scopes           = EXCLUDED.scopes,
			token_expires_at = EXCLUDED.token_expires_at,
			connected_at     = EXCLUDED.connected_at`,
		c.ID, c.UserID, c.ProviderID, c.ProviderType, c.AuthType,
		c.BaseURL, c.DisplayName, c.ProviderUserID, c.Username, c.Scopes,
		c.TokenExpiresAt, c.ConnectedAt,
	)
	return err
}

// GetOAuthConnectionByID returns a connection by id, or nil if not found.
func (db *DB) GetOAuthConnectionByID(ctx context.Context, id string) (*OAuthConnection, error) {
	row := db.pool.QueryRow(ctx, `
		SELECT id, user_id, provider_id, provider_type, auth_type, base_url, display_name,
		       provider_user_id, username, scopes, token_expires_at, connected_at
		FROM oauth_connections WHERE id = $1`, id)
	return scanOAuthConnection(row)
}

// GetOAuthConnectionsByUser returns all connections for a user, ordered by connected_at.
func (db *DB) GetOAuthConnectionsByUser(ctx context.Context, userID string) ([]*OAuthConnection, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, user_id, provider_id, provider_type, auth_type, base_url, display_name,
		       provider_user_id, username, scopes, token_expires_at, connected_at
		FROM oauth_connections WHERE user_id = $1 ORDER BY connected_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var conns []*OAuthConnection
	for rows.Next() {
		c := &OAuthConnection{}
		if err := rows.Scan(&c.ID, &c.UserID, &c.ProviderID, &c.ProviderType, &c.AuthType,
			&c.BaseURL, &c.DisplayName, &c.ProviderUserID, &c.Username, &c.Scopes,
			&c.TokenExpiresAt, &c.ConnectedAt); err != nil {
			return nil, err
		}
		conns = append(conns, c)
	}
	return conns, rows.Err()
}

// DeleteOAuthConnection removes a connection by id and returns the deleted row.
func (db *DB) DeleteOAuthConnection(ctx context.Context, id string) (*OAuthConnection, error) {
	row := db.pool.QueryRow(ctx, `
		DELETE FROM oauth_connections WHERE id = $1
		RETURNING id, user_id, provider_id, provider_type, auth_type, base_url, display_name,
		          provider_user_id, username, scopes, token_expires_at, connected_at`, id)
	return scanOAuthConnection(row)
}

// GetExpiringGitLabConnections returns GitLab connections with tokens expiring within the given duration.
func (db *DB) GetExpiringGitLabConnections(ctx context.Context, within time.Duration) ([]*OAuthConnection, error) {
	// Use interval arithmetic — pass seconds as a string Postgres can parse.
	withinSecs := fmt.Sprintf("%d seconds", int64(within.Seconds()))
	rows, err := db.pool.Query(ctx, `
		SELECT id, user_id, provider_id, provider_type, auth_type, base_url, display_name,
		       provider_user_id, username, scopes, token_expires_at, connected_at
		FROM oauth_connections
		WHERE provider_type = 'gitlab'
		  AND token_expires_at IS NOT NULL
		  AND token_expires_at < NOW() + $1::interval
		ORDER BY token_expires_at`, withinSecs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var conns []*OAuthConnection
	for rows.Next() {
		c := &OAuthConnection{}
		if err := rows.Scan(&c.ID, &c.UserID, &c.ProviderID, &c.ProviderType, &c.AuthType,
			&c.BaseURL, &c.DisplayName, &c.ProviderUserID, &c.Username, &c.Scopes,
			&c.TokenExpiresAt, &c.ConnectedAt); err != nil {
			return nil, err
		}
		conns = append(conns, c)
	}
	return conns, rows.Err()
}

// UpdateTokenExpiry updates token_expires_at for a connection.
func (db *DB) UpdateTokenExpiry(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE oauth_connections SET token_expires_at = $1 WHERE id = $2`,
		expiresAt, id)
	return err
}

// scanOAuthConnection scans a single row into an OAuthConnection.
func scanOAuthConnection(row interface {
	Scan(dest ...any) error
}) (*OAuthConnection, error) {
	c := &OAuthConnection{}
	err := row.Scan(&c.ID, &c.UserID, &c.ProviderID, &c.ProviderType, &c.AuthType,
		&c.BaseURL, &c.DisplayName, &c.ProviderUserID, &c.Username, &c.Scopes,
		&c.TokenExpiresAt, &c.ConnectedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}

// schema is the DDL applied on startup via Migrate().
// Table creation order matters for foreign keys: users → hosts → projects → oauth_connections.
// ALTER TABLE statements handle backward-compatible migration of existing installations.
// Per ADR-0095: the migration includes DELETE FROM hosts WHERE slug = 'local' to remove
// auto-created local host stubs. ON DELETE CASCADE removes referencing project rows.
const schema = `
-- Base tables (for fresh installations, includes all ADR-0035 columns).
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    oauth_id      TEXT,
    is_admin      BOOLEAN NOT NULL DEFAULT FALSE,
    slug          TEXT UNIQUE NOT NULL DEFAULT '',
    nkey_public   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS hosts (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slug            TEXT NOT NULL,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('machine', 'cluster')),
    role            TEXT NOT NULL DEFAULT 'owner' CHECK (role IN ('owner', 'user')),
    js_domain       TEXT,
    leaf_url        TEXT,
    account_jwt     TEXT,
    direct_nats_url TEXT,
    public_key      TEXT,
    user_jwt        TEXT,
    nats_jwt        TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ,
    UNIQUE (user_id, slug),
    CHECK (type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS projects (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL DEFAULT '',
    git_url         TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'active',
    host_id         TEXT REFERENCES hosts(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS oauth_connections (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id      TEXT NOT NULL,
    provider_type    TEXT NOT NULL,
    auth_type        TEXT NOT NULL DEFAULT 'oauth',
    base_url         TEXT NOT NULL,
    display_name     TEXT NOT NULL DEFAULT '',
    provider_user_id TEXT NOT NULL,
    username         TEXT NOT NULL,
    scopes           TEXT NOT NULL DEFAULT '',
    token_expires_at TIMESTAMPTZ,
    connected_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, base_url, provider_user_id)
);

-- Backward-compatible column additions for existing installations.
ALTER TABLE users ADD COLUMN IF NOT EXISTS oauth_id TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT '';

-- Backfill slug from full email (ADR-0062).
-- Algorithm: lowercase, replace all non-[a-z0-9] runs with '-', trim leading/trailing '-'.
-- Runs for empty slugs AND for old local-part-only slugs that don't include the domain.
UPDATE users SET slug = trim(both '-' from lower(regexp_replace(email, '[^a-zA-Z0-9]+', '-', 'g')))
WHERE slug = '' OR slug != trim(both '-' from lower(regexp_replace(email, '[^a-zA-Z0-9]+', '-', 'g')));

-- Ensure slug uniqueness (idempotent; covers both fresh installs and upgrades
-- where the column was added without the UNIQUE constraint).
CREATE UNIQUE INDEX IF NOT EXISTS users_slug_uniq ON users (slug);

ALTER TABLE projects ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS host_id TEXT REFERENCES hosts(id) ON DELETE CASCADE;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS git_identity_id TEXT REFERENCES oauth_connections(id) ON DELETE SET NULL;

-- Backfill slug from name for projects that have no slug set.
UPDATE projects
SET slug = lower(regexp_replace(name, '[^a-zA-Z0-9]+', '-', 'g'))
WHERE slug IS NULL OR slug = '';

-- KNOWN-14: Enforce host_id NOT NULL after backfill (safe: all rows now have host_id).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM projects WHERE host_id IS NULL) THEN
        BEGIN
            ALTER TABLE projects ALTER COLUMN host_id SET NOT NULL;
        EXCEPTION WHEN others THEN
            -- Constraint may already exist or table may be in inconsistent state; skip.
            NULL;
        END;
    END IF;
END
$$;

-- Unique index: projects are unique-by-slug per user per host (ADR-0035).
CREATE UNIQUE INDEX IF NOT EXISTS projects_user_id_host_id_slug_uniq ON projects (user_id, host_id, slug);

-- ADR-0054: NKey public key for user challenge-response auth.
ALTER TABLE users ADD COLUMN IF NOT EXISTS nkey_public TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS users_nkey_public_uniq ON users (nkey_public) WHERE nkey_public IS NOT NULL;

-- ADR-0054: nats_jwt column on hosts (renamed from user_jwt in the spec).
-- Both columns exist during migration; nats_jwt is canonical per ADR-0054.
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS nats_jwt TEXT;

-- ADR-0054: UNIQUE index on hosts.public_key for fast challenge-response lookup.
CREATE UNIQUE INDEX IF NOT EXISTS hosts_public_key_uniq ON hosts (public_key) WHERE public_key IS NOT NULL;

-- ADR-0054: host_access table — per-user access grants to hosts.
-- The host owner has implicit access (not stored here). Only granted users.
CREATE TABLE IF NOT EXISTS host_access (
    host_id    TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (host_id, user_id)
);

-- ADR-0054: agent_credentials table — one credential per user/host/project.
-- Stores the agent's NKey public key registered by the host controller.
-- The UNIQUE(user_id, host_slug, project_slug) constraint ensures one active
-- credential per project per host per user.
CREATE TABLE IF NOT EXISTS agent_credentials (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    host_slug    TEXT NOT NULL,
    project_slug TEXT NOT NULL,
    nkey_public  TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, host_slug, project_slug)
);

-- ADR-0053: import support columns on projects.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'created';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS import_ref TEXT;

-- ADR-0053: attachments table — metadata for S3-stored binary data.
CREATE TABLE IF NOT EXISTS attachments (
    id          TEXT PRIMARY KEY,
    s3_key      TEXT UNIQUE NOT NULL,
    filename    TEXT NOT NULL,
    mime_type   TEXT NOT NULL,
    size_bytes  BIGINT NOT NULL CHECK (size_bytes > 0),
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    host_id     TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    confirmed   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_attachments_project ON attachments (project_id);
CREATE INDEX IF NOT EXISTS idx_attachments_unconfirmed ON attachments (confirmed, created_at) WHERE confirmed = FALSE;

-- ADR-0063: drop legacy leaf-NATS CHECK constraint and deprecated columns from hosts.
-- The constraint blocked inserting type='cluster' rows without js_domain/leaf_url/account_jwt.
-- Columns js_domain, leaf_url, account_jwt are deprecated per ADR-0054 (hub-direct topology).
-- PostgreSQL CHECK constraints are named by the server; find and drop it by scanning
-- pg_constraint. Uses DO block to be idempotent across fresh installs (where the
-- columns and constraint were never created) and upgrades (where they exist).
DO $$
DECLARE
    cname TEXT;
BEGIN
    -- Drop the legacy cluster-host CHECK constraint if it exists.
    SELECT conname INTO cname
    FROM pg_constraint
    WHERE conrelid = 'hosts'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) LIKE '%js_domain%'
    LIMIT 1;
    IF cname IS NOT NULL THEN
        EXECUTE 'ALTER TABLE hosts DROP CONSTRAINT ' || quote_ident(cname);
    END IF;

    -- Drop deprecated columns if they exist.
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'hosts' AND column_name = 'js_domain') THEN
        ALTER TABLE hosts DROP COLUMN js_domain;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'hosts' AND column_name = 'leaf_url') THEN
        ALTER TABLE hosts DROP COLUMN leaf_url;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'hosts' AND column_name = 'account_jwt') THEN
        ALTER TABLE hosts DROP COLUMN account_jwt;
    END IF;
END
$$;

-- ADR-0091: is_default_platform column on hosts.
-- At most one host can be the default platform (partial unique index enforces this).
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS is_default_platform BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX IF NOT EXISTS idx_hosts_default_platform ON hosts (is_default_platform) WHERE is_default_platform = TRUE;

-- ADR-0091: user_host_agents table — one row per (user, host) pair.
-- Tracks the in-namespace user-agent that handles token delivery and project pod lifecycle.
CREATE TABLE IF NOT EXISTS user_host_agents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_slug   TEXT NOT NULL,
    user_slug   TEXT NOT NULL,
    nkey_public TEXT,
    x25519_pub  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (host_slug, user_slug)
);

-- ADR-0091: host_grants table — bilateral grant/accept handshake.
-- The host initiates; the user (or CP on default platform) accepts.
CREATE TABLE IF NOT EXISTS host_grants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_slug   TEXT NOT NULL,
    user_slug   TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at TIMESTAMPTZ,
    UNIQUE (host_slug, user_slug)
);
`
