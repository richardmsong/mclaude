package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog"
)

const (
	// jwtRefreshCheckInterval is how often the daemon checks JWT TTL.
	jwtRefreshCheckInterval = 60 * time.Second
	// jwtRefreshThreshold is the minimum remaining TTL before refresh.
	jwtRefreshThreshold = 15 * time.Minute
	// laptopHeartbeatInterval is how often the daemon writes its laptop KV entry.
	laptopHeartbeatInterval = 12 * time.Hour
	// childRestartDelay is the wait between a child crash and restart.
	childRestartDelay = 2 * time.Second
)

// DaemonConfig holds the runtime configuration for --daemon mode.
type DaemonConfig struct {
	NATSCredsFile   string
	RefreshURL      string // POST /auth/refresh endpoint
	UserID          string
	Hostname        string
	MachineID       string
	AgentBinary     string // path to this binary (os.Args[0])
	AgentArgs       []string
	Log             zerolog.Logger
	CredentialsPath string // path to ~/.claude/.credentials.json; default "$HOME/.claude/.credentials.json"
}

// Daemon is the laptop launcher — manages one child session-agent per project.
type Daemon struct {
	cfg        DaemonConfig
	nc         *nats.Conn
	js         jetstream.JetStream
	mu         sync.Mutex
	children   map[string]*managedChild
	laptopsKV  jetstream.KeyValue
	sessKV     jetstream.KeyValue  // mclaude-sessions — read-only for startup recovery
	jobQueueKV jetstream.KeyValue  // mclaude-job-queue — read/write for dispatcher
	projectsKV jetstream.KeyValue  // mclaude-projects — read-only for GET /jobs/projects
	quotaCh    chan QuotaStatus    // quota publisher -> job dispatcher
}

// managedChild tracks a running child session-agent process.
type managedChild struct {
	projectID string
	cmd       *exec.Cmd
	stopCh    chan struct{}
}

// laptopEntry is the value stored in mclaude-laptops KV.
type laptopEntry struct {
	MachineID string `json:"machineId"`
	TS        string `json:"ts"`
}

// NewDaemon creates a Daemon connected to NATS.
func NewDaemon(nc *nats.Conn, cfg DaemonConfig) (*Daemon, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}

	ctx := context.Background()
	laptopsKV, err := js.KeyValue(ctx, "mclaude-laptops")
	if err != nil {
		return nil, fmt.Errorf("mclaude-laptops KV not found (control-plane not started?): %w", err)
	}
	sessKV, err := js.KeyValue(ctx, "mclaude-sessions")
	if err != nil {
		return nil, fmt.Errorf("mclaude-sessions KV not found (control-plane not started?): %w", err)
	}
	jobQueueKV, err := js.KeyValue(ctx, "mclaude-job-queue")
	if err != nil {
		return nil, fmt.Errorf("mclaude-job-queue KV not found (control-plane not started?): %w", err)
	}
	projectsKV, err := js.KeyValue(ctx, "mclaude-projects")
	if err != nil {
		return nil, fmt.Errorf("mclaude-projects KV not found (control-plane not started?): %w", err)
	}

	return &Daemon{
		cfg:        cfg,
		nc:         nc,
		js:         js,
		children:   make(map[string]*managedChild),
		laptopsKV:  laptopsKV,
		sessKV:     sessKV,
		jobQueueKV: jobQueueKV,
		projectsKV: projectsKV,
		quotaCh:    make(chan QuotaStatus, 8),
	}, nil
}

// Run starts the daemon: hostname check, subscription, JWT refresh loop.
// Blocks until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.checkHostnameCollision(ctx); err != nil {
		return fmt.Errorf("hostname collision check: %w", err)
	}

	if err := d.writeLaptopKV(ctx); err != nil {
		d.cfg.Log.Warn().Err(err).Msg("failed to write laptop KV entry on startup (non-fatal)")
	}

	createSubject := fmt.Sprintf("mclaude.%s.api.projects.create", d.cfg.UserID)
	sub, err := d.nc.Subscribe(createSubject, d.handleProjectCreate)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", createSubject, err)
	}
	defer sub.Drain()

	go d.runJWTRefresh(ctx)
	go d.runLaptopHeartbeat(ctx)
	go d.runQuotaPublisher(ctx)
	go d.runLifecycleSubscriber(ctx)
	go d.runJobDispatcher(ctx)
	go d.runJobsHTTP(ctx)

	<-ctx.Done()
	d.shutdownChildren()
	return nil
}

// checkHostnameCollision returns an error if another machine has registered
// the same hostname with a different machineID.
func (d *Daemon) checkHostnameCollision(ctx context.Context) error {
	key := fmt.Sprintf("%s.%s", d.cfg.UserID, d.cfg.Hostname)
	entry, err := d.laptopsKV.Get(ctx, key)
	if err != nil {
		// Key absent or transient error — no collision.
		return nil
	}
	var existing laptopEntry
	if err := json.Unmarshal(entry.Value(), &existing); err != nil {
		return nil
	}
	if existing.MachineID != "" && existing.MachineID != d.cfg.MachineID {
		return fmt.Errorf(
			"hostname %q is already registered to another machine (machineId=%s) — "+
				"set a unique hostname with: mclaude config hostname <name>",
			d.cfg.Hostname, existing.MachineID,
		)
	}
	return nil
}

// writeLaptopKV writes or refreshes the laptop registration KV entry.
func (d *Daemon) writeLaptopKV(ctx context.Context) error {
	key := fmt.Sprintf("%s.%s", d.cfg.UserID, d.cfg.Hostname)
	entry := laptopEntry{
		MachineID: d.cfg.MachineID,
		TS:        time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(entry)
	_, err := d.laptopsKV.Put(ctx, key, data)
	return err
}

// runLaptopHeartbeat refreshes the laptop KV entry every 12h.
func (d *Daemon) runLaptopHeartbeat(ctx context.Context) {
	tick := time.NewTicker(laptopHeartbeatInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := d.writeLaptopKV(ctx); err != nil {
				d.cfg.Log.Warn().Err(err).Msg("laptop KV refresh failed")
			}
		}
	}
}

// handleProjectCreate spawns a child session-agent for the new project.
func (d *Daemon) handleProjectCreate(msg *nats.Msg) {
	var req struct {
		ProjectID string `json:"projectId"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		d.cfg.Log.Warn().Err(err).Msg("daemon: failed to parse project create payload")
		return
	}
	projectID := req.ProjectID
	if projectID == "" {
		projectID = req.ID
	}
	if projectID == "" {
		d.cfg.Log.Warn().Msg("daemon: project create payload missing projectId")
		return
	}

	d.mu.Lock()
	_, already := d.children[projectID]
	d.mu.Unlock()
	if already {
		d.cfg.Log.Debug().Str("projectId", projectID).Msg("daemon: child already running for project")
		return
	}

	d.cfg.Log.Info().Str("projectId", projectID).Msg("daemon: spawning child for project")
	d.spawnChild(projectID)
}

// spawnChild starts a supervised child for the given project.
func (d *Daemon) spawnChild(projectID string) {
	child := &managedChild{
		projectID: projectID,
		stopCh:    make(chan struct{}),
	}
	d.mu.Lock()
	d.children[projectID] = child
	d.mu.Unlock()

	go d.manageChild(child)
}

// manageChild runs the child process, restarting on crash until stopCh closes.
func (d *Daemon) manageChild(child *managedChild) {
	for {
		select {
		case <-child.stopCh:
			d.cfg.Log.Info().Str("projectId", child.projectID).Msg("daemon: child stop requested")
			return
		default:
		}

		cmd := d.buildChildCmd(child.projectID)
		d.mu.Lock()
		child.cmd = cmd
		d.mu.Unlock()

		d.cfg.Log.Info().Str("projectId", child.projectID).Msg("daemon: starting child")
		if err := cmd.Start(); err != nil {
			d.cfg.Log.Error().Err(err).Str("projectId", child.projectID).Msg("daemon: child start failed")
		} else {
			if err := cmd.Wait(); err != nil {
				d.cfg.Log.Warn().Err(err).Str("projectId", child.projectID).Msg("daemon: child crashed — restarting")
			}
		}

		select {
		case <-child.stopCh:
			return
		case <-time.After(childRestartDelay):
		}
	}
}

// buildChildCmd constructs the exec.Cmd for a child session-agent.
func (d *Daemon) buildChildCmd(projectID string) *exec.Cmd {
	args := append(append([]string{}, d.cfg.AgentArgs...),
		"--project-id", projectID,
		"--user-id", d.cfg.UserID,
	)
	cmd := exec.Command(d.cfg.AgentBinary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd
}

// shutdownChildren interrupts all running children and marks them stopped.
func (d *Daemon) shutdownChildren() {
	d.mu.Lock()
	children := make([]*managedChild, 0, len(d.children))
	for _, c := range d.children {
		children = append(children, c)
	}
	d.mu.Unlock()

	for _, c := range children {
		// Signal stop before interrupting so manageChild exits the restart loop.
		select {
		case <-c.stopCh:
		default:
			close(c.stopCh)
		}
		d.mu.Lock()
		cmd := c.cmd
		d.mu.Unlock()
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
	}
}

// runJWTRefresh checks JWT TTL every jwtRefreshCheckInterval and refreshes
// when TTL falls below jwtRefreshThreshold.
func (d *Daemon) runJWTRefresh(ctx context.Context) {
	tick := time.NewTicker(jwtRefreshCheckInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			d.maybeRefreshJWT(ctx)
		}
	}
}

// maybeRefreshJWT reads the creds file, checks TTL, and refreshes if needed.
func (d *Daemon) maybeRefreshJWT(ctx context.Context) {
	if d.cfg.NATSCredsFile == "" || d.cfg.RefreshURL == "" {
		return
	}

	remaining, err := jwtRemainingTTL(d.cfg.NATSCredsFile)
	if err != nil {
		d.cfg.Log.Warn().Err(err).Msg("daemon: failed to read JWT TTL from creds file")
		return
	}
	if remaining > jwtRefreshThreshold {
		return
	}

	d.cfg.Log.Info().
		Str("remaining", remaining.Round(time.Second).String()).
		Msg("daemon: JWT TTL below threshold — refreshing")

	if err := d.refreshJWT(ctx); err != nil {
		d.cfg.Log.Warn().Err(err).Msg("daemon: JWT refresh failed — children will use current JWT until expiry")
	} else {
		d.cfg.Log.Info().Msg("daemon: JWT refreshed successfully")
	}
}

// refreshJWT POSTs to /auth/refresh and updates the creds file with the new JWT.
func (d *Daemon) refreshJWT(ctx context.Context) error {
	currentJWT, err := readJWTFromCredsFile(d.cfg.NATSCredsFile)
	if err != nil {
		return fmt.Errorf("read current JWT: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.RefreshURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+currentJWT)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", d.cfg.RefreshURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("refresh returned %d: %s", resp.StatusCode, body)
	}

	var refreshResp struct {
		JWT string `json:"jwt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refreshResp); err != nil {
		return fmt.Errorf("decode refresh response: %w", err)
	}
	if refreshResp.JWT == "" {
		return fmt.Errorf("refresh response missing jwt field")
	}

	return writeJWTToCredsFile(d.cfg.NATSCredsFile, refreshResp.JWT)
}

// jwtRemainingTTL reads the creds file, parses the JWT, and returns the TTL.
func jwtRemainingTTL(credsFile string) (time.Duration, error) {
	jwtStr, err := readJWTFromCredsFile(credsFile)
	if err != nil {
		return 0, err
	}
	return jwtTTL(jwtStr)
}

// readJWTFromCredsFile reads a NATS credentials file and extracts the JWT.
// NATS .creds format:
//
//	-----BEGIN NATS USER JWT-----
//	<base64 JWT>
//	------END NATS USER JWT------
func readJWTFromCredsFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read creds file: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	inJWT := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "BEGIN NATS USER JWT") {
			inJWT = true
			continue
		}
		if strings.Contains(trimmed, "END NATS USER JWT") {
			break
		}
		if inJWT && trimmed != "" {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("no JWT found in creds file %s", filepath.Base(path))
}

// jwtTTL decodes a JWT (without verifying the signature) and returns the TTL.
func jwtTTL(jwtStr string) (time.Duration, error) {
	p := jwt.NewParser(jwt.WithoutClaimsValidation())
	claims := jwt.MapClaims{}
	_, _, err := p.ParseUnverified(jwtStr, claims)
	if err != nil {
		return 0, fmt.Errorf("parse JWT: %w", err)
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return 0, fmt.Errorf("JWT missing exp claim")
	}
	remaining := time.Until(exp.Time)
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// writeJWTToCredsFile replaces the JWT section in a NATS creds file with newJWT.
func writeJWTToCredsFile(path, newJWT string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read creds file for update: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var out []string
	inJWT := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "BEGIN NATS USER JWT") {
			inJWT = true
			out = append(out, line)
			out = append(out, newJWT)
			continue
		}
		if strings.Contains(trimmed, "END NATS USER JWT") {
			inJWT = false
			out = append(out, line)
			continue
		}
		if inJWT {
			// Skip old JWT content.
			continue
		}
		out = append(out, line)
	}

	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0600)
}
