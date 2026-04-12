package main

import (
	"encoding/json"
	"net/http"
	"os"
)

// VersionResponse is the payload returned by GET /version.
type VersionResponse struct {
	// MinClientVersion is the minimum SPA/CLI version required to connect.
	// Clients below this version must upgrade before use (feature X4).
	MinClientVersion string `json:"minClientVersion"`
	// ServerVersion is this control-plane's own version (git tag or commit).
	ServerVersion string `json:"serverVersion,omitempty"`
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	minVersion := os.Getenv("MIN_CLIENT_VERSION")
	if minVersion == "" {
		minVersion = "0.0.0"
	}
	serverVersion := os.Getenv("SERVER_VERSION")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(VersionResponse{ //nolint:errcheck
		MinClientVersion: minVersion,
		ServerVersion:    serverVersion,
	})
}
