package server

import cpdb "mclaude-control-plane/db"

// Type aliases for DB types — allows server package code to use these types
// without the cpdb. prefix. This preserves the existing code style while
// keeping the DB package separate.

// ConnectDB is a package-level alias for db.ConnectDB, allowing server package
// code (including tests) to call ConnectDB without the cpdb. prefix.
var ConnectDB = cpdb.ConnectDB

// DB is an alias for db.DB (the database connection pool).
type DB = cpdb.DB

// User is an alias for db.User (a row from the users table).
type User = cpdb.User

// Host is an alias for db.Host (a row from the hosts table).
type Host = cpdb.Host

// Project is an alias for db.Project (a row from the projects table).
type Project = cpdb.Project

// AgentCredential is an alias for db.AgentCredential.
type AgentCredential = cpdb.AgentCredential

// UserHostAgent is an alias for db.UserHostAgent.
type UserHostAgent = cpdb.UserHostAgent

// HostGrant is an alias for db.HostGrant.
type HostGrant = cpdb.HostGrant

// Attachment is an alias for db.Attachment.
type Attachment = cpdb.Attachment

// OAuthConnection is an alias for db.OAuthConnection.
type OAuthConnection = cpdb.OAuthConnection
