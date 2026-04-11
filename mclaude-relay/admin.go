package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// HandleAuthMe returns the authenticated user's info.
func (r *Relay) HandleAuthMe(w http.ResponseWriter, req *http.Request) {
	user := r.authenticateRequest(req)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      user.ID,
		"name":    user.Name,
		"email":   user.Email,
		"role":    user.Role,
		"laptops": user.Laptops,
	})
}

// HandleAdminUsers handles CRUD operations on users. Requires admin role.
func (r *Relay) HandleAdminUsers(w http.ResponseWriter, req *http.Request) {
	user := r.authenticateRequest(req)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != "admin" {
		http.Error(w, "forbidden: admin role required", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	path := req.URL.Path

	// POST /admin/users — create user
	if path == "/admin/users" && req.Method == "POST" {
		r.adminCreateUser(w, req)
		return
	}

	// GET /admin/users — list users
	if path == "/admin/users" && req.Method == "GET" {
		r.adminListUsers(w, req)
		return
	}

	// Routes with user ID: /admin/users/{id} or /admin/users/{id}/rotate-token
	trimmed := strings.TrimPrefix(path, "/admin/users/")
	if trimmed == path {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	parts := strings.SplitN(trimmed, "/", 2)
	userID := parts[0]

	if len(parts) == 2 && parts[1] == "rotate-token" && req.Method == "POST" {
		r.adminRotateToken(w, userID)
		return
	}

	if len(parts) == 1 {
		switch req.Method {
		case "PUT":
			r.adminUpdateUser(w, req, userID)
			return
		case "DELETE":
			r.adminDeleteUser(w, userID)
			return
		case "GET":
			r.adminGetUser(w, userID)
			return
		}
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (r *Relay) adminListUsers(w http.ResponseWriter, req *http.Request) {
	users := r.users.ListUsers()
	// Redact tokens — show only last 8 chars
	type redactedUser struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Email    string   `json:"email,omitempty"`
		Token    string   `json:"token"`
		Laptops  []string `json:"laptops"`
		Role     string   `json:"role"`
		Source   string   `json:"source,omitempty"`
		Disabled bool     `json:"disabled,omitempty"`
	}
	result := make([]redactedUser, len(users))
	for i, u := range users {
		tok := u.Token
		if len(tok) > 8 {
			tok = "…" + tok[len(tok)-8:]
		}
		result[i] = redactedUser{
			ID:       u.ID,
			Name:     u.Name,
			Email:    u.Email,
			Token:    tok,
			Laptops:  u.Laptops,
			Role:     u.Role,
			Source:   u.Source,
			Disabled: u.Disabled,
		}
	}
	json.NewEncoder(w).Encode(result)
}

func (r *Relay) adminCreateUser(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(io.LimitReader(req.Body, 1024*1024))
	if err != nil {
		http.Error(w, "body read error", http.StatusBadRequest)
		return
	}
	var input struct {
		Name    string   `json:"name"`
		Email   string   `json:"email"`
		Role    string   `json:"role"`
		Laptops []string `json:"laptops"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if input.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if input.Role == "" {
		input.Role = "user"
	}
	if input.Laptops == nil {
		input.Laptops = []string{}
	}

	u := r.users.CreateUser(input.Name, input.Email, input.Role, input.Laptops)

	w.WriteHeader(http.StatusCreated)
	// Return full token on creation only
	json.NewEncoder(w).Encode(u)
}

func (r *Relay) adminUpdateUser(w http.ResponseWriter, req *http.Request, id string) {
	body, err := io.ReadAll(io.LimitReader(req.Body, 1024*1024))
	if err != nil {
		http.Error(w, "body read error", http.StatusBadRequest)
		return
	}
	var input struct {
		Name     string   `json:"name"`
		Email    string   `json:"email"`
		Role     string   `json:"role"`
		Laptops  []string `json:"laptops"`
		Disabled *bool    `json:"disabled"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	u := r.users.UpdateUser(id, input.Name, input.Email, input.Role, input.Laptops, input.Disabled)
	if u == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	// Redact token in response
	resp := map[string]interface{}{
		"id":      u.ID,
		"name":    u.Name,
		"email":   u.Email,
		"role":    u.Role,
		"laptops": u.Laptops,
		"source":  u.Source,
	}
	if u.Disabled {
		resp["disabled"] = true
	}
	json.NewEncoder(w).Encode(resp)
}

func (r *Relay) adminDeleteUser(w http.ResponseWriter, id string) {
	if !r.users.DeleteUser(id) {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (r *Relay) adminGetUser(w http.ResponseWriter, id string) {
	u := r.users.GetUser(id)
	if u == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	tok := u.Token
	if len(tok) > 8 {
		tok = "…" + tok[len(tok)-8:]
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":       u.ID,
		"name":     u.Name,
		"email":    u.Email,
		"token":    tok,
		"laptops":  u.Laptops,
		"role":     u.Role,
		"source":   u.Source,
		"disabled": u.Disabled,
	})
}

func (r *Relay) adminRotateToken(w http.ResponseWriter, id string) {
	u := r.users.RotateToken(id)
	if u == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	// Return full new token
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":    u.ID,
		"name":  u.Name,
		"token": u.Token,
	})
}
