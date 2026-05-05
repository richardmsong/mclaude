package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	relaypkg "mclaude-relay/relay"
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
	users := relaypkg.NewUserStore(usersFile)

	// Bootstrap: if user store is empty AND WEB_TOKEN is set, seed an admin user
	if users.IsEmpty() && webToken != "" {
		admin := users.CreateUserWithToken("Admin", "", "admin", webToken, []string{"*"})
		log.Printf("Bootstrap: created admin user '%s' (id=%s) with WEB_TOKEN", admin.Name, admin.ID)
	}

	tunnelStatic := os.Getenv("TUNNEL_STATIC") == "1" || os.Getenv("TUNNEL_STATIC") == "true"
	tunnelStaticHost := os.Getenv("TUNNEL_STATIC_HOST") // which laptop serves static files

	r := relaypkg.New(tunnelToken, webToken, users)

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
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path

		// CORS preflight
		if req.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Filename, X-Laptop-ID")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Tunnel endpoint — connector registers here
		if path == "/tunnel" {
			r.HandleTunnel(w, req)
			return
		}

		// Auth endpoint — returns current user info
		if path == "/auth/me" {
			r.HandleAuthMe(w, req)
			return
		}

		// Admin endpoints — user management
		if strings.HasPrefix(path, "/admin/") {
			r.HandleAdminUsers(w, req)
			return
		}

		// PTY WebSocket endpoint — browser shell connects here
		if path == "/ws/pty" {
			r.HandlePtyWS(w, req)
			return
		}

		// WebSocket endpoint — phone/browser connects here
		if path == "/ws" {
			r.HandleClientWS(w, req)
			return
		}

		// Health endpoint (no auth)
		if path == "/health" {
			laptops := r.ConnectedLaptops()
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
			r.HandleLaptops(w, req)
			return
		}

		// API proxy
		if isAPIPath(path) {
			r.HandleAPI(w, req)
			return
		}

		// Static web app — either tunnel to connector or serve locally
		if tunnelStatic {
			host := tunnelStaticHost
			if host == "" {
				host = "default"
			}
			r.HandleTunnelStatic(w, req, host)
		} else {
			fileServer.ServeHTTP(w, req)
		}
	})

	log.Printf("mclaude-relay listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
