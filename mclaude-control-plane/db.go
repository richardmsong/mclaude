package main

import (
	"context"
	"fmt"
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
	CreatedAt    time.Time
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
		`SELECT id, email, name, password_hash, created_at FROM users WHERE email = $1`,
		email)
	u := &User{}
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt)
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
		`SELECT id, email, name, password_hash, created_at FROM users WHERE id = $1`,
		id)
	u := &User{}
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt)
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
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx,
		`INSERT INTO users (id, email, name, password_hash, created_at) VALUES ($1, $2, $3, $4, $5)`,
		id, email, name, passwordHash, now)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &User{ID: id, Email: email, Name: name, PasswordHash: passwordHash, CreatedAt: now}, nil
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
	GitURL        string
	Status        string
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
		`SELECT id, user_id, name, git_url, status, created_at FROM projects WHERE user_id = $1 ORDER BY created_at`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("get projects: %w", err)
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		p := &Project{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.GitURL, &p.Status, &p.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// GetProjectByID returns a project by ID, or nil if not found.
func (db *DB) GetProjectByID(ctx context.Context, id string) (*Project, error) {
	row := db.pool.QueryRow(ctx,
		`SELECT id, user_id, name, git_url, status, created_at FROM projects WHERE id = $1`, id)
	p := &Project{}
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.GitURL, &p.Status, &p.CreatedAt)
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
const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS projects (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    git_url    TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
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

ALTER TABLE projects ADD COLUMN IF NOT EXISTS git_identity_id TEXT REFERENCES oauth_connections(id) ON DELETE SET NULL;
`
