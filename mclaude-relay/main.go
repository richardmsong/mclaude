package main

import (
	"embed"
	"encoding/json"
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

	if tunnelToken == "" {
		log.Fatal("TUNNEL_TOKEN env var is required")
	}

	// User store: load from file or create empty
	usersFile := os.Getenv("USERS_FILE")
	if usersFile == "" {
		usersFile = "/tmp/mclaude-users.json"
	}
	users := NewUserStore(usersFile)

	// Bootstrap: if user store is empty AND WEB_TOKEN is set, seed an admin user
	if users.IsEmpty() && webToken != "" {
		admin := users.CreateUserWithToken("Admin", "", "admin", webToken, []string{"*"})
		log.Printf("Bootstrap: created admin user '%s' (id=%s) with WEB_TOKEN", admin.Name, admin.ID)
	}

	tunnelStatic := os.Getenv("TUNNEL_STATIC") == "1" || os.Getenv("TUNNEL_STATIC") == "true"
	tunnelStaticHost := os.Getenv("TUNNEL_STATIC_HOST") // which laptop serves static files

	relay := NewRelay(tunnelToken, webToken, users)

	// Static file serving strategy:
	// 1. TUNNEL_STATIC=true → relay proxies static requests through tunnel to connector
	//    TUNNEL_STATIC_HOST pins to a specific laptop (default: any available)
	// 2. STATIC_DIR or ./static on disk → serve from disk (hot-reload on VM)
	// 3. Fallback → serve from embedded binary
	var fileServer http.Handler
	if tunnelStatic {
		if tunnelStaticHost != "" {
			log.Printf("Static files will be served via tunnel from host: %s", tunnelStaticHost)
		} else {
			log.Printf("Static files will be served via tunnel (any host)")
		}
	} else {
		staticDir := os.Getenv("STATIC_DIR")
		if staticDir == "" {
			staticDir = "static"
		}
		if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
			log.Printf("Serving static files from disk: %s (hot-reload enabled)", staticDir)
			fileServer = http.FileServer(http.Dir(staticDir))
		} else {
			log.Printf("Serving static files from embedded binary")
			subFS, err := fs.Sub(staticFiles, "static")
			if err != nil {
				log.Fatalf("static fs: %v", err)
			}
			fileServer = http.FileServer(http.FS(subFS))
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// CORS preflight
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Filename, X-Laptop-ID")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Tunnel endpoint — connector registers here
		if path == "/tunnel" {
			relay.HandleTunnel(w, r)
			return
		}

		// Auth endpoint — returns current user info
		if path == "/auth/me" {
			relay.HandleAuthMe(w, r)
			return
		}

		// Admin endpoints — user management
		if strings.HasPrefix(path, "/admin/") {
			relay.HandleAdminUsers(w, r)
			return
		}

		// PTY WebSocket endpoint — browser shell connects here
		if path == "/ws/pty" {
			relay.HandlePtyWS(w, r)
			return
		}

		// WebSocket endpoint — phone/browser connects here
		if path == "/ws" {
			relay.HandleClientWS(w, r)
			return
		}

		// Health endpoint (no auth)
		if path == "/health" {
			laptops := relay.ConnectedLaptops()
			w.Header().Set("Content-Type", "application/json")
			data := map[string]interface{}{
				"status":  "ok",
				"tunnels": len(laptops),
				"laptops": laptops,
			}
			json.NewEncoder(w).Encode(data) //nolint:errcheck
			return
		}

		// Laptops endpoint — filter by user ACL if authenticated
		if path == "/laptops" {
			laptops := relay.ConnectedLaptops()
			// If user is authenticated, filter by their ACL
			if user := relay.authenticateRequest(r); user != nil {
				var filtered []string
				for _, h := range laptops {
					if relay.userCanAccessLaptop(user, h) {
						filtered = append(filtered, h)
					}
				}
				laptops = filtered
			}
			w.Header().Set("Content-Type", "application/json")
			type laptopInfo struct {
				ID        string `json:"id"`
				Connected bool   `json:"connected"`
			}
			result := make([]laptopInfo, len(laptops))
			for i, l := range laptops {
				result[i] = laptopInfo{ID: l, Connected: true}
			}
			json.NewEncoder(w).Encode(result) //nolint:errcheck
			return
		}

		// API proxy
		if isAPIPath(path) {
			relay.HandleAPI(w, r)
			return
		}

		// Static web app — either tunnel to connector or serve locally
		if tunnelStatic {
			host := tunnelStaticHost
			if host == "" {
				host = "default"
			}
			relay.HandleTunnelStatic(w, r, host)
		} else {
			fileServer.ServeHTTP(w, r)
		}
	})

	log.Printf("mclaude-relay listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
