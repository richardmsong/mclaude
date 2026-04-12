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

// schema is the DDL applied on startup via Migrate().
const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`
