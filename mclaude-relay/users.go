package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"sync"
)

// User represents an authenticated user with laptop-level access control.
type User struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Email    string   `json:"email,omitempty"`
	Token    string   `json:"token"`
	Laptops  []string `json:"laptops"`
	Role     string   `json:"role"`
	Source   string   `json:"source,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

// UserStore provides thread-safe user management with JSON file persistence.
type UserStore struct {
	mu    sync.RWMutex
	users map[string]*User // id -> user
	path  string
}

// NewUserStore loads users from the given JSON file path, or creates an empty store.
func NewUserStore(path string) *UserStore {
	s := &UserStore{
		users: make(map[string]*User),
		path:  path,
	}
	s.load()
	return s
}

// Authenticate looks up a user by bearer token. Returns nil if not found or disabled.
func (s *UserStore) Authenticate(token string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Token == token && !u.Disabled {
			return u
		}
	}
	return nil
}

// CanAccessLaptop checks if a user has access to the given laptop hostname.
// A laptops list of ["*"] grants access to all laptops.
func (s *UserStore) CanAccessLaptop(user *User, hostname string) bool {
	if user == nil {
		return false
	}
	for _, l := range user.Laptops {
		if l == "*" || l == hostname {
			return true
		}
	}
	return false
}

// CreateUser creates a new user with a generated token and persists the store.
func (s *UserStore) CreateUser(name, email, role string, laptops []string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()

	u := &User{
		ID:      generateID(),
		Name:    name,
		Email:   email,
		Token:   generateToken(),
		Laptops: laptops,
		Role:    role,
		Source:  "managed",
	}
	s.users[u.ID] = u
	s.save()
	return u
}

// CreateUserWithToken creates a user with a specific token (used for bootstrap).
func (s *UserStore) CreateUserWithToken(name, email, role, token string, laptops []string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()

	u := &User{
		ID:      generateID(),
		Name:    name,
		Email:   email,
		Token:   token,
		Laptops: laptops,
		Role:    role,
		Source:  "managed",
	}
	s.users[u.ID] = u
	s.save()
	return u
}

// UpdateUser updates fields on an existing user. Only non-zero fields are applied.
func (s *UserStore) UpdateUser(id string, name, email, role string, laptops []string, disabled *bool) *User {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[id]
	if !ok {
		return nil
	}
	if name != "" {
		u.Name = name
	}
	if email != "" {
		u.Email = email
	}
	if role != "" {
		u.Role = role
	}
	if laptops != nil {
		u.Laptops = laptops
	}
	if disabled != nil {
		u.Disabled = *disabled
	}
	s.save()
	return u
}

// DeleteUser removes a user by ID and persists the store.
func (s *UserStore) DeleteUser(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return false
	}
	delete(s.users, id)
	s.save()
	return true
}

// ListUsers returns all users.
func (s *UserStore) ListUsers() []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		list = append(list, u)
	}
	return list
}

// GetUser returns a user by ID, or nil if not found.
func (s *UserStore) GetUser(id string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.users[id]
}

// RotateToken generates a new token for the given user and persists the store.
func (s *UserStore) RotateToken(id string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return nil
	}
	u.Token = generateToken()
	s.save()
	return u
}

// IsEmpty returns true if the store has no users.
func (s *UserStore) IsEmpty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users) == 0
}

// save persists users to the JSON file. Must be called with mu held.
func (s *UserStore) save() {
	data, err := json.MarshalIndent(s.users, "", "  ")
	if err != nil {
		log.Printf("UserStore save marshal error: %v", err)
		return
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		log.Printf("UserStore save write error: %v", err)
	}
}

// load reads users from the JSON file. Called once at startup.
func (s *UserStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("UserStore load error: %v", err)
		}
		return
	}
	if err := json.Unmarshal(data, &s.users); err != nil {
		log.Printf("UserStore load unmarshal error: %v", err)
	}
}

// generateToken creates a new bearer token: mcu_ + 32 random hex bytes.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "mcu_" + hex.EncodeToString(b)
}

// generateID creates an 8-character random hex ID.
func generateID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
