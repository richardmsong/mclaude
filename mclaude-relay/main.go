package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
)

//go:embed static
var staticFiles embed.FS

var apiPrefixes = []string{
	"/sessions", "/projects", "/skills",
	"/screenshots", "/files", "/telemetry",
	"/auth/",
}

func isAPIPath(path string) bool {
	for _, prefix := range apiPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func main() {
	tunnelToken := os.Getenv("TUNNEL_TOKEN")
	webToken := os.Getenv("WEB_TOKEN")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	if tunnelToken == "" || webToken == "" {
		log.Fatal("TUNNEL_TOKEN and WEB_TOKEN env vars are required")
	}

	relay := NewRelay(tunnelToken, webToken)

	subFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// CORS preflight
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Filename")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Tunnel endpoint — connector registers here
		if path == "/tunnel" {
			relay.HandleTunnel(w, r)
			return
		}

		// WebSocket endpoint — phone/browser connects here
		if path == "/ws" {
			relay.HandleClientWS(w, r)
			return
		}

		// Relay-native health (no auth, no proxy)
		if path == "/health" {
			relay.mu.RLock()
			connected := relay.tunnel != nil
			relay.mu.RUnlock()
			status := "disconnected"
			if connected {
				status = "connected"
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"status":"ok","tunnel":"%s"}`, status)
			return
		}

		// API proxy
		if isAPIPath(path) {
			relay.HandleAPI(w, r)
			return
		}

		// Static web app
		fileServer.ServeHTTP(w, r)
	})

	log.Printf("mclaude-relay listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
