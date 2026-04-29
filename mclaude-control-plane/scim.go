package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// KNOWN-20: Basic SCIM 2.0 /scim/v2/Users endpoints per RFC 7644.
// Protected by the same admin bearer token as admin endpoints.

// SCIMUser represents a SCIM 2.0 User resource.
type SCIMUser struct {
	Schemas  []string       `json:"schemas"`
	ID       string         `json:"id"`
	UserName string         `json:"userName"`
	Name     *SCIMName      `json:"name,omitempty"`
	Emails   []SCIMEmail    `json:"emails,omitempty"`
	Active   bool           `json:"active"`
	Meta     *SCIMMeta      `json:"meta,omitempty"`
}

// SCIMName is the SCIM name sub-resource.
type SCIMName struct {
	Formatted  string `json:"formatted,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

// SCIMEmail is a SCIM email entry.
type SCIMEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
	Type    string `json:"type,omitempty"`
}

// SCIMMeta is the SCIM meta sub-resource.
type SCIMMeta struct {
	ResourceType string `json:"resourceType"`
	Location     string `json:"location,omitempty"`
}

// SCIMListResponse is the SCIM 2.0 list response.
type SCIMListResponse struct {
	Schemas      []string   `json:"schemas"`
	TotalResults int        `json:"totalResults"`
	Resources    []SCIMUser `json:"Resources"`
}

// SCIMPatchOp is a SCIM PATCH operation.
type SCIMPatchOp struct {
	Schemas    []string          `json:"schemas"`
	Operations []SCIMPatchOpItem `json:"Operations"`
}

// SCIMPatchOpItem is a single SCIM PATCH operation.
type SCIMPatchOpItem struct {
	Op    string      `json:"op"`
	Path  string      `json:"path,omitempty"`
	Value interface{} `json:"value,omitempty"`
}

// handleSCIMRoutes dispatches /scim/v2/* requests.
func (s *Server) handleSCIMRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/scim/v2/")

	switch {
	case strings.HasPrefix(path, "Users"):
		userPath := strings.TrimPrefix(path, "Users")
		userPath = strings.TrimPrefix(userPath, "/")

		switch {
		case r.Method == http.MethodGet && userPath == "":
			s.scimListUsers(w, r)
		case r.Method == http.MethodGet && userPath != "":
			s.scimGetUser(w, r, userPath)
		case r.Method == http.MethodPost && userPath == "":
			s.scimCreateUser(w, r)
		case r.Method == http.MethodPatch && userPath != "":
			s.scimPatchUser(w, r, userPath)
		case r.Method == http.MethodDelete && userPath != "":
			s.scimDeleteUser(w, r, userPath)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func userToSCIM(u *User) SCIMUser {
	return SCIMUser{
		Schemas:  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		ID:       u.ID,
		UserName: u.Email,
		Name: &SCIMName{
			Formatted: u.Name,
		},
		Emails: []SCIMEmail{
			{Value: u.Email, Primary: true, Type: "work"},
		},
		Active: true, // all users are active (no soft-delete)
		Meta: &SCIMMeta{
			ResourceType: "User",
		},
	}
}

// scimListUsers handles GET /scim/v2/Users.
func (s *Server) scimListUsers(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Support filter=userName eq "email" per RFC 7644 §3.4.2.2.
	filter := r.URL.Query().Get("filter")
	var users []*User

	if filter != "" && strings.Contains(filter, "userName eq") {
		// Parse: userName eq "value"
		parts := strings.SplitN(filter, `"`, 3)
		if len(parts) >= 2 {
			email := parts[1]
			user, err := s.db.GetUserByEmail(r.Context(), email)
			if err == nil && user != nil {
				users = []*User{user}
			}
		}
	} else {
		// List all users.
		rows, err := s.db.pool.Query(r.Context(),
			`SELECT id, email, name, password_hash, oauth_id, is_admin, slug, created_at
			 FROM users ORDER BY created_at`)
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		for rows.Next() {
			u := &User{}
			if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.OAuthID, &u.IsAdmin, &u.Slug, &u.CreatedAt); err != nil {
				http.Error(w, "scan error", http.StatusInternalServerError)
				return
			}
			users = append(users, u)
		}
	}

	resources := make([]SCIMUser, 0, len(users))
	for _, u := range users {
		resources = append(resources, userToSCIM(u))
	}

	resp := SCIMListResponse{
		Schemas:      []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
		TotalResults: len(resources),
		Resources:    resources,
	}

	w.Header().Set("Content-Type", "application/scim+json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// scimGetUser handles GET /scim/v2/Users/{id}.
func (s *Server) scimGetUser(w http.ResponseWriter, r *http.Request, id string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	user, err := s.db.GetUserByID(r.Context(), id)
	if err != nil || user == nil {
		w.Header().Set("Content-Type", "application/scim+json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:Error"},
			"detail":  "User not found",
			"status":  404,
		})
		return
	}

	w.Header().Set("Content-Type", "application/scim+json")
	json.NewEncoder(w).Encode(userToSCIM(user)) //nolint:errcheck
}

// scimCreateUser handles POST /scim/v2/Users.
func (s *Server) scimCreateUser(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	var scimUser SCIMUser
	if err := json.NewDecoder(r.Body).Decode(&scimUser); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	email := scimUser.UserName
	if email == "" && len(scimUser.Emails) > 0 {
		email = scimUser.Emails[0].Value
	}
	if email == "" {
		http.Error(w, "userName or emails[0].value is required", http.StatusBadRequest)
		return
	}

	name := ""
	if scimUser.Name != nil {
		name = scimUser.Name.Formatted
		if name == "" {
			name = strings.TrimSpace(scimUser.Name.GivenName + " " + scimUser.Name.FamilyName)
		}
	}
	if name == "" {
		name = email
	}

	id := uuid.NewString()
	user, err := s.db.CreateUser(r.Context(), id, email, name, "")
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			w.Header().Set("Content-Type", "application/scim+json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:Error"},
				"detail":  "User already exists",
				"status":  409,
			})
			return
		}
		http.Error(w, "create user error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(userToSCIM(user)) //nolint:errcheck
}

// scimPatchUser handles PATCH /scim/v2/Users/{id}.
func (s *Server) scimPatchUser(w http.ResponseWriter, r *http.Request, id string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	user, err := s.db.GetUserByID(r.Context(), id)
	if err != nil || user == nil {
		w.Header().Set("Content-Type", "application/scim+json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:Error"},
			"detail":  "User not found",
			"status":  404,
		})
		return
	}

	var patch SCIMPatchOp
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	for _, op := range patch.Operations {
		switch strings.ToLower(op.Op) {
		case "replace":
			switch {
			case op.Path == "active":
				// Active=false means deactivate. We treat this as a no-op since we don't
				// have a soft-delete mechanism, but log it.
				if val, ok := op.Value.(bool); ok && !val {
					log.Info().Str("userId", id).Msg("SCIM: deactivate user (no-op — hard delete via DELETE)")
				}
			case op.Path == "displayName" || op.Path == "name.formatted":
				if val, ok := op.Value.(string); ok && val != "" {
					_, _ = s.db.pool.Exec(r.Context(),
						`UPDATE users SET name = $1 WHERE id = $2`, val, id)
					user.Name = val
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/scim+json")
	json.NewEncoder(w).Encode(userToSCIM(user)) //nolint:errcheck
}

// scimDeleteUser handles DELETE /scim/v2/Users/{id}.
func (s *Server) scimDeleteUser(w http.ResponseWriter, r *http.Request, id string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := s.db.DeleteUser(r.Context(), id); err != nil {
		http.Error(w, "delete error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
