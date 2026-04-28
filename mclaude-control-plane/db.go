package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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
	Slug         string // URL-safe identifier derived from email local-part (ADR-0046)
	CreatedAt    time.Time
}

// slugNonAlphaNum matches any run of characters that are not alphanumeric.
var slugNonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// computeUserSlug derives a URL-safe slug from an email address.
// Uses the local part (before '@'), lowercased, with non-alphanumeric runs
// replaced by '-'. Consistent with the SQL backfill in the schema migration.
func computeUserSlug(email string) string {
	local := strings.Split(email, "@")[0]
	return strings.ToLower(slugNonAlphaNum.ReplaceAllString(local, "-"))
}

// Host is a row from the hosts table. Per ADR-0035, this is the single source
// of truth for both BYOH machines and K8s clusters.
type Host struct {
	ID            string
	UserID        string
	Slug          string
	Name          string
	Type          string  // 'machine' or 'cluster'
	Role          string  // 'owner' or 'user'
	JsDomain      *string // NULL for machine hosts
	LeafURL       *string // NULL for machine hosts
	AccountJWT    *string // NULL for machine hosts
	DirectNATSURL *string // Optional even for cluster hosts
	PublicKey     *string
	UserJWT       *string
	CreatedAt     time.Time
	LastSeenAt    *time.Time
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

// GetUserByEmail looks up a user by email address. Returns nil, nil if not found.
func (db *DB) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, oauth_id, is_admin, slug, created_at FROM users WHERE email = $1`,
		email)
	u := &User{}
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.OAuthID, &u.IsAdmin, &u.Slug, &u.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

// GetUserByID looks up a user by ID. Returns nil, nil if not found.
func (db *DB) GetUserByID(ctx context.Context, id string) (*User, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash, oauth_id, is_admin, slug, created_at FROM users WHERE id = $1`,
		id)
	u := &User{}
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.OAuthID, &u.IsAdmin, &u.Slug, &u.CreatedAt)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

// CreateUser inserts a new user row. id must be a pre-generated UUID.
func (db *DB) CreateUser(ctx context.Context, id, email, name, passwordHash string) (*User, error) {
	slug := computeUserSlug(email)
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx,
		`INSERT INTO users (id, email, name, password_hash, slug, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, email, name, passwordHash, slug, now)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &User{ID: id, Email: email, Name: name, PasswordHash: passwordHash, Slug: slug, CreatedAt: now}, nil
}

// SetUserAdmin sets or clears the is_admin flag on a user.
func (db *DB) SetUserAdmin(ctx context.Context, id string, isAdmin bool) error {
	_, err := db.pool.Exec(ctx, `UPDATE users SET is_admin = $1 WHERE id = $2`, isAdmin, id)
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
	GitIdentityID *string
	CreatedAt     time.Time
}

// CreateProject inserts a new project row. id must be a pre-generated UUID.
func (db *DB) CreateProject(ctx context.Context, id, userID, name, gitURL string) (*Project, error) {
	return db.CreateProjectWithIdentity(ctx, id, userID, name, gitURL, nil)
}

// CreateProjectWithIdentity inserts a new project row with optional git_identity_id.
func (db *DB) CreateProjectWithIdentity(ctx context.Context, id, userID, name, gitURL string, gitIdentityID *string) (*Project, error) {
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx,
		`INSERT INTO projects (id, user_id, name, git_url, status, created_at, git_identity_id) VALUES ($1, $2, $3, $4, 'active', $5, $6)`,
		id, userID, name, gitURL, now, gitIdentityID)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	return &Project{ID: id, UserID: userID, Name: name, GitURL: gitURL, Status: "active", GitIdentityID: gitIdentityID, CreatedAt: now}, nil
}

// GetProjectsByUser returns all projects owned by a user.
func (db *DB) GetProjectsByUser(ctx context.Context, userID string) ([]*Project, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, user_id, name, slug, git_url, status, host_id, created_at, git_identity_id FROM projects WHERE user_id = $1 ORDER BY created_at`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("get projects: %w", err)
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Slug, &p.GitURL, &p.Status, &p.HostID, &p.CreatedAt, &p.GitIdentityID); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// GetProjectByID returns a project by ID, or nil if not found.
func (db *DB) GetProjectByID(ctx context.Context, id string) (*Project, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, user_id, name, slug, git_url, status, host_id, created_at, git_identity_id FROM projects WHERE id = $1`, id)
	p := &Project{}
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Slug, &p.GitURL, &p.Status, &p.HostID, &p.CreatedAt, &p.GitIdentityID)
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
// The DO block backfills a default 'local' machine host per user and sets host_id on projects.
const schema = `
-- Base tables (for fresh installations, includes all ADR-0035 columns).
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    oauth_id      TEXT,
    is_admin      BOOLEAN NOT NULL DEFAULT FALSE,
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

-- Backfill slug from email local-part for any existing rows where slug is empty.
UPDATE users SET slug = lower(regexp_replace(split_part(email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g')) WHERE slug = '';

ALTER TABLE projects ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS host_id TEXT REFERENCES hosts(id) ON DELETE CASCADE;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS git_identity_id TEXT REFERENCES oauth_connections(id) ON DELETE SET NULL;

-- Backfill: create a default 'local' machine host for every user that lacks one,
-- then set host_id on orphaned projects.
DO $$
DECLARE
    u RECORD;
    hid TEXT;
BEGIN
    FOR u IN SELECT usr.id FROM users usr WHERE NOT EXISTS (
        SELECT 1 FROM hosts WHERE hosts.user_id = usr.id AND hosts.slug = 'local'
    ) LOOP
        hid := gen_random_uuid()::text;
        INSERT INTO hosts (id, user_id, slug, name, type, role)
        VALUES (hid, u.id, 'local', 'Local Machine', 'machine', 'owner');
    END LOOP;
END
$$;

UPDATE projects p
SET host_id = h.id
FROM hosts h
WHERE p.user_id = h.user_id AND h.slug = 'local' AND p.host_id IS NULL;

-- Backfill slug from name for projects that have no slug set.
UPDATE projects
SET slug = lower(regexp_replace(name, '[^a-zA-Z0-9]+', '-', 'g'))
WHERE slug IS NULL OR slug = '';

-- Unique index: projects are unique-by-slug per user per host (ADR-0035).
CREATE UNIQUE INDEX IF NOT EXISTS projects_user_id_host_id_slug_uniq ON projects (user_id, host_id, slug);
`
