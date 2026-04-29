package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"mclaude.io/common/pkg/slug"
	"mclaude.io/common/pkg/subj"
)

const (
	// quotaPollInterval is how often the quota publisher polls the Anthropic API.
	quotaPollInterval = 60 * time.Second
	// jobDispatchPollTimeout is how long to wait for a session to become idle.
	jobDispatchPollTimeout = 30 * time.Second
	// jobDispatchPollInterval is how often to check session state.
	jobDispatchPollInterval = 500 * time.Millisecond
	// jobsHTTPAddr is the loopback address for the jobs HTTP server.
	jobsHTTPAddr = "localhost:8378"
	// maxJobStartRetries is how many times to retry starting a job before failing.
	maxJobStartRetries = 3
)

// credentialsFile holds the parsed ~/.claude/.credentials.json structure.
type credentialsFile struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

// quotaAPIResponse is the parsed response from api.anthropic.com/api/oauth/usage.
type quotaAPIResponse struct {
	FiveHour struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"five_hour"`
	SevenDay struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"seven_day"`
}

// readOAuthToken reads the OAuth access token from the credentials file.
func readOAuthToken(credentialsPath string) (string, error) {
	if credentialsPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		credentialsPath = filepath.Join(home, ".claude", ".credentials.json")
	}
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		return "", fmt.Errorf("read credentials: %w", err)
	}
	var creds credentialsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", fmt.Errorf("parse credentials: %w", err)
	}
	if creds.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("no accessToken in credentials file")
	}
	return creds.ClaudeAiOauth.AccessToken, nil
}

// fetchQuotaStatus polls the Anthropic OAuth usage API and returns a QuotaStatus.
func fetchQuotaStatus(credentialsPath string) QuotaStatus {
	token, err := readOAuthToken(credentialsPath)
	if err != nil {
		return QuotaStatus{HasData: false}
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return QuotaStatus{HasData: false}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return QuotaStatus{HasData: false}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return QuotaStatus{HasData: false}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return QuotaStatus{HasData: false}
	}

	var apiResp quotaAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return QuotaStatus{HasData: false}
	}

	var r5, r7 time.Time
	if t, err := time.Parse(time.RFC3339, apiResp.FiveHour.ResetsAt); err == nil {
		r5 = t
	}
	if t, err := time.Parse(time.RFC3339, apiResp.SevenDay.ResetsAt); err == nil {
		r7 = t
	}

	return QuotaStatus{
		HasData: true,
		U5:      int(apiResp.FiveHour.Utilization * 100),
		R5:      r5,
		U7:      int(apiResp.SevenDay.Utilization * 100),
		R7:      r7,
		TS:      time.Now().UTC(),
	}
}

// runQuotaPublisher polls the Anthropic quota API every 60s and publishes
// QuotaStatus to mclaude.{userId}.quota (core NATS). Also sends to the
// internal quotaCh for the job dispatcher.
func (d *Daemon) runQuotaPublisher(ctx context.Context) {
	subject := subj.UserQuota(d.cfg.UserSlug)

	publish := func() {
		qs := fetchQuotaStatus(d.cfg.CredentialsPath)
		data, _ := json.Marshal(qs)
		_ = d.nc.Publish(subject, data)
		// Non-blocking send to dispatcher.
		select {
		case d.quotaCh <- qs:
		default:
		}
	}

	// Publish immediately on startup.
	publish()

	tick := time.NewTicker(quotaPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			publish()
		}
	}
}

// jobProjectSlug returns the project slug from a job entry, falling back to
// slugifying the ProjectID if no explicit slug is set (spec: GAP-SA-K19).
func jobProjectSlug(job *JobEntry) slug.ProjectSlug {
	if job.ProjectSlug != "" {
		return slug.ProjectSlug(job.ProjectSlug)
	}
	s := slug.Slugify(job.ProjectID)
	if s == "" {
		s = "p-" + job.ProjectID[:8]
	}
	return slug.ProjectSlug(s)
}

// specPathToComponent maps a spec path to a dev-harness component argument.
func specPathToComponent(specPath string) string {
	base := filepath.Base(specPath)
	// Strip "plan-" prefix and ".md" suffix.
	base = strings.TrimPrefix(base, "plan-")
	base = strings.TrimSuffix(base, ".md")
	switch base {
	case "spa", "client-architecture":
		return "spa"
	case "session-agent":
		return "session-agent"
	case "k8s-integration", "github-oauth":
		return "control-plane"
	default:
		return "all"
	}
}

// specPathToSlug derives the job branch slug from a spec path.
// e.g. "docs/plan-spa.md" -> "spa"
func specPathToSlug(specPath string) string {
	base := filepath.Base(specPath)
	base = strings.TrimPrefix(base, "plan-")
	base = strings.TrimSuffix(base, ".md")
	return base
}

// scheduledSessionPrompt builds the initial dev-harness prompt for a job.
func scheduledSessionPrompt(specPath, component, priority, branch string, otherJobs []string) string {
	concurrent := "none"
	if len(otherJobs) > 0 {
		concurrent = strings.Join(otherJobs, "\n")
	}
	return fmt.Sprintf(`You are running as an unattended scheduled dev-harness session.

Spec: %s
Component: %s
Priority: %s
Branch: %s

Concurrent sessions also running:
%s

Instructions:
1. Run: /dev-harness %s
2. When your work is complete (all spec gaps closed, all tests passing), run:
   gh pr create --base main --head %s --title "feat(%s): scheduled dev-harness [auto]" --body "Auto-created by scheduled dev-harness session for %s."
   Then output on its own line: SESSION_JOB_COMPLETE:{prUrl}
3. If you receive a message starting with QUOTA_THRESHOLD_REACHED, immediately:
   a. Finish your current task and commit.
   b. Run: /dev-harness %s --audit-only
   c. Output the full gap report.
   d. Stop without starting new work.`,
		specPath, component, priority, branch,
		concurrent,
		component,
		branch, component, specPath,
		component,
	)
}

// readJobEntry reads and unmarshals a JobEntry from jobQueueKV by {uslug}.{jobId}.
// Uses the daemon's typed UserSlug (not UUID) for the key (spec: GAP-SA-K18).
func (d *Daemon) readJobEntry(userID, jobID string) (*JobEntry, jetstream.KeyValueEntry, error) {
	key := subj.JobQueueKVKey(d.cfg.UserSlug, jobID)
	entry, err := d.jobQueueKV.Get(context.Background(), key)
	if err != nil {
		return nil, nil, fmt.Errorf("get job %s: %w", key, err)
	}
	var job JobEntry
	if err := json.Unmarshal(entry.Value(), &job); err != nil {
		return nil, nil, fmt.Errorf("unmarshal job %s: %w", key, err)
	}
	return &job, entry, nil
}

// writeJobEntry marshals and writes a JobEntry to jobQueueKV.
// Uses the daemon's typed UserSlug (not UUID) for the key (spec: GAP-SA-K18).
func (d *Daemon) writeJobEntry(job *JobEntry) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	key := subj.JobQueueKVKey(d.cfg.UserSlug, job.ID)
	_, err = d.jobQueueKV.Put(context.Background(), key, data)
	return err
}

// runLifecycleSubscriber subscribes to mclaude.{userId}.*.lifecycle.* and
// updates jobQueueKV on terminal job lifecycle events.
func (d *Daemon) runLifecycleSubscriber(ctx context.Context) {
	// Subscribe to lifecycle events for this user on this host only (spec: GAP-SA-N6).
	// New format: mclaude.users.{uslug}.hosts.{hslug}.projects.*.lifecycle.* (ADR-0035)
	subject := "mclaude.users." + string(d.cfg.UserSlug) + ".hosts." + string(d.cfg.HostSlug) + ".projects.*.lifecycle.*"
	sub, err := d.nc.Subscribe(subject, func(msg *nats.Msg) {
		var ev map[string]string
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			return
		}
		evType := ev["type"]
		jobID := ev["jobId"]
		if jobID == "" {
			return // not a job lifecycle event
		}

		job, _, err := d.readJobEntry(d.cfg.UserID, jobID)
		if err != nil {
			return // unrecognized jobId — ignore silently
		}

		switch evType {
		case "session_job_complete":
			job.Status = "completed"
			job.PRUrl = ev["prUrl"]
			now := time.Now().UTC()
			job.CompletedAt = &now

		case "session_job_paused":
			// session_job_paused is published by both the QuotaMonitor and the
			// dispatcher. When the dispatcher already wrote state, this is a no-op.
			if job.Status == "paused" {
				d.cfg.Log.Info().
					Str("jobId", jobID).
					Msg("daemon: session_job_paused received (state already set by dispatcher)")
				return
			}
			if job.AutoContinue {
				job.Status = "paused"
				if r5Str := ev["r5"]; r5Str != "" {
					if t, err := time.Parse(time.RFC3339, r5Str); err == nil {
						job.ResumeAt = &t
					}
				}
			} else {
				job.Status = "queued"
			}

		case "session_permission_denied":
			job.Status = "needs_spec_fix"
			job.FailedTool = ev["tool"]

		case "session_job_failed":
			job.Status = "failed"
			job.Error = ev["error"]

		default:
			return
		}

		if err := d.writeJobEntry(job); err != nil {
			d.cfg.Log.Warn().Err(err).Str("jobId", jobID).Str("type", evType).Msg("daemon: failed to update job KV")
		}
	})
	if err != nil {
		d.cfg.Log.Warn().Err(err).Str("subject", subject).Msg("daemon: lifecycle subscriber failed to subscribe")
		return
	}
	defer sub.Drain()
	<-ctx.Done()
}

// dispatchQueuedJob starts a session for a queued job entry.
// Returns the session ID on success or an error.
func (d *Daemon) dispatchQueuedJob(job *JobEntry) error {
	// Derive branch and component.
	jobSlug := specPathToSlug(job.SpecPath)
	shortID := job.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	branch := fmt.Sprintf("schedule/%s-%s", jobSlug, shortID)
	job.Branch = branch

	component := specPathToComponent(job.SpecPath)

	// Mark as starting and persist.
	job.Status = "starting"
	if err := d.writeJobEntry(job); err != nil {
		return fmt.Errorf("write starting status: %w", err)
	}

	// Build the sessions.create request.
	createSubject := subj.UserHostProjectAPISessionsCreate(d.cfg.UserSlug, d.cfg.HostSlug, jobProjectSlug(job))
	createPayload, _ := json.Marshal(map[string]any{
		"branch":    branch,
		"permPolicy": "strict-allowlist",
		"quotaMonitor": map[string]any{
			"threshold":    job.Threshold,
			"priority":     job.Priority,
			"jobId":        job.ID,
			"autoContinue": job.AutoContinue,
		},
	})

	// Send sessions.create with 10s timeout.
	reply, err := d.nc.Request(createSubject, createPayload, 10*time.Second)
	if err != nil {
		return fmt.Errorf("sessions.create request: %w", err)
	}

	var createResp struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(reply.Data, &createResp); err != nil {
		return fmt.Errorf("parse sessions.create reply: %w", err)
	}
	if createResp.Error != "" {
		return fmt.Errorf("sessions.create error: %s", createResp.Error)
	}
	sessionID := createResp.ID
	if sessionID == "" {
		return fmt.Errorf("sessions.create returned empty session ID")
	}

	// Poll sessKV for state=idle (up to 30s).
	// Use slug-based KV key; sessionSlug falls back to sessionID (pre-migration entries).
	kvKey := subj.SessionsKVKey(d.cfg.UserSlug, d.cfg.HostSlug, jobProjectSlug(job), slug.SessionSlug(sessionID))
	deadline := time.Now().Add(jobDispatchPollTimeout)
	for time.Now().Before(deadline) {
		entry, err := d.sessKV.Get(context.Background(), kvKey)
		if err == nil {
			var st SessionState
			if json.Unmarshal(entry.Value(), &st) == nil && st.State == StateIdle {
				break
			}
		}
		select {
		case <-time.After(jobDispatchPollInterval):
		}
	}
	// Check final state.
	entry, err := d.sessKV.Get(context.Background(), kvKey)
	if err != nil {
		job.RetryCount++
		job.Status = "queued"
		job.SessionID = ""
		if job.RetryCount >= maxJobStartRetries {
			job.Status = "failed"
			job.Error = fmt.Sprintf("session failed to start after %d attempts", maxJobStartRetries)
		}
		_ = d.writeJobEntry(job)
		return fmt.Errorf("session never reached idle: %w", err)
	}
	var st SessionState
	if json.Unmarshal(entry.Value(), &st) != nil || st.State != StateIdle {
		job.RetryCount++
		job.Status = "queued"
		job.SessionID = ""
		if job.RetryCount >= maxJobStartRetries {
			job.Status = "failed"
			job.Error = fmt.Sprintf("session failed to start after %d attempts", maxJobStartRetries)
		}
		_ = d.writeJobEntry(job)
		return fmt.Errorf("session state %q after poll timeout", st.State)
	}

	// Collect other running jobs for context in the prompt.
	var otherSpecs []string
	watcher, _ := d.jobQueueKV.WatchAll(context.Background())
	if watcher != nil {
		for e := range watcher.Updates() {
			if e == nil {
				break
			}
			var other JobEntry
			if json.Unmarshal(e.Value(), &other) == nil &&
				other.UserID == d.cfg.UserID &&
				other.ID != job.ID &&
				(other.Status == "running" || other.Status == "queued") {
				otherSpecs = append(otherSpecs, other.SpecPath)
			}
		}
		watcher.Stop()
	}

	// Send the dev-harness prompt via sessions.input.
	prompt := scheduledSessionPrompt(job.SpecPath, component, fmt.Sprintf("%d", job.Priority), branch, otherSpecs)
	inputSubject := subj.UserHostProjectAPISessionsInput(d.cfg.UserSlug, d.cfg.HostSlug, jobProjectSlug(job))
	inputPayload, _ := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": prompt,
		},
		"session_id": sessionID,
	})
	_ = d.nc.Publish(inputSubject, inputPayload)

	// Mark as running.
	now := time.Now().UTC()
	job.Status = "running"
	job.SessionID = sessionID
	job.StartedAt = &now
	return d.writeJobEntry(job)
}

// startupRecovery handles jobs in intermediate states from a previous daemon run.
func (d *Daemon) startupRecovery() {
	watcher, err := d.jobQueueKV.WatchAll(context.Background())
	if err != nil {
		d.cfg.Log.Warn().Err(err).Msg("daemon: startup recovery watchAll failed")
		return
	}
	defer watcher.Stop()

	for entry := range watcher.Updates() {
		if entry == nil {
			break
		}
		if entry.Operation() != jetstream.KeyValuePut {
			continue
		}
		var job JobEntry
		if json.Unmarshal(entry.Value(), &job) != nil {
			continue
		}
		if job.UserID != d.cfg.UserID {
			continue
		}

		switch job.Status {
		case "starting":
			// Session create was sent but never reached running. Reset to queued.
			job.Status = "queued"
			job.SessionID = ""
			_ = d.writeJobEntry(&job)

		case "running":
			// Check if the session still exists in sessKV.
			if job.SessionID != "" {
				kvKey := subj.SessionsKVKey(d.cfg.UserSlug, d.cfg.HostSlug, jobProjectSlug(&job), slug.SessionSlug(job.SessionID))
				_, err := d.sessKV.Get(context.Background(), kvKey)
				if err != nil {
					// Session gone — reset to queued.
					job.Status = "queued"
					job.SessionID = ""
					_ = d.writeJobEntry(&job)
				}
			}

		case "paused":
			// Check if ResumeAt has passed.
			if job.ResumeAt != nil && job.ResumeAt.Before(time.Now()) {
				job.Status = "queued"
				job.ResumeAt = nil
				_ = d.writeJobEntry(&job)
			}
		}
	}
}

// runJobDispatcher watches the job queue and quota status, dispatching and
// pausing jobs as appropriate.
func (d *Daemon) runJobDispatcher(ctx context.Context) {
	d.startupRecovery()

	var lastQuota QuotaStatus

	// Watch KV for new/updated jobs.
	watcher, err := d.jobQueueKV.WatchAll(ctx)
	if err != nil {
		d.cfg.Log.Warn().Err(err).Msg("daemon: job dispatcher watchAll failed")
		return
	}
	defer watcher.Stop()

	// Channel that merges KV changes and quota updates.
	type trigger struct {
		job   *JobEntry
		quota *QuotaStatus
	}
	triggerCh := make(chan trigger, 32)

	// Forward KV watcher updates.
	go func() {
		for entry := range watcher.Updates() {
			if entry == nil {
				continue
			}
			if entry.Operation() != jetstream.KeyValuePut {
				continue
			}
			var job JobEntry
			if json.Unmarshal(entry.Value(), &job) != nil {
				continue
			}
			if job.UserID != d.cfg.UserID {
				continue
			}
			select {
			case triggerCh <- trigger{job: &job}:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Forward quota updates.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case qs := <-d.quotaCh:
				select {
				case triggerCh <- trigger{quota: &qs}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-triggerCh:
			if t.quota != nil {
				lastQuota = *t.quota
			}
			d.processDispatch(ctx, lastQuota, t.job)
		}
	}
}

// processDispatch handles a single dispatcher trigger (quota update or job change).
func (d *Daemon) processDispatch(ctx context.Context, quota QuotaStatus, changedJob *JobEntry) {
	// If quota threshold exceeded, pause lowest-priority running jobs.
	if quota.HasData {
		// Collect all running jobs.
		var running []*JobEntry
		watcher, err := d.jobQueueKV.WatchAll(ctx)
		if err == nil {
			for entry := range watcher.Updates() {
				if entry == nil {
					break
				}
				var job JobEntry
				if json.Unmarshal(entry.Value(), &job) != nil {
					continue
				}
				if job.UserID == d.cfg.UserID && job.Status == "running" {
					jobCopy := job
					running = append(running, &jobCopy)
				}
			}
			watcher.Stop()
		}

		// Check if any running job's threshold is exceeded.
		anyExceeded := false
		for _, job := range running {
			if quota.U5 >= job.Threshold {
				anyExceeded = true
				break
			}
		}

		if anyExceeded {
			// Sort by priority ascending (lowest priority first = first to be paused).
			// Spec: stop jobs in ascending priority order, applying 5% hysteresis —
			// stop enough sessions that accumulated headroom drops below threshold-5.
			// Since each job carries its own Threshold (no shared headroom to sum),
			// exact headroom cannot be computed; per the spec fallback we stop all
			// running jobs whose per-job threshold is exceeded (u5 >= job.Threshold).
			sort.Slice(running, func(i, j int) bool {
				return running[i].Priority < running[j].Priority
			})
			for _, job := range running {
				if quota.U5 < job.Threshold {
					break // this job's threshold not exceeded; higher-threshold jobs also safe
				}
				// Send graceful stop.
				inputSubject := subj.UserHostProjectAPISessionsInput(d.cfg.UserSlug, d.cfg.HostSlug, jobProjectSlug(job))
				stopMsg, _ := json.Marshal(map[string]any{
					"type": "user",
					"message": map[string]any{
						"role":    "user",
						"content": "QUOTA_THRESHOLD_REACHED: The 5-hour API quota threshold has been reached. Please finish your current task and commit all changes, run --audit-only to generate a gap report and output the full results, then stop. Do not start any new tasks.",
					},
					"session_id": job.SessionID,
				})
				_ = d.nc.Publish(inputSubject, stopMsg)

				// Publish session_job_paused lifecycle event (spec: GAP-SA-K13).
				lifecycleSubject := subj.UserHostProjectLifecycle(d.cfg.UserSlug, d.cfg.HostSlug, jobProjectSlug(job), slug.SessionSlug(job.SessionID))
				pausedPayload, _ := json.Marshal(map[string]any{
					"type":                      "session_job_paused",
					"sessionId":                 job.SessionID,
					"jobId":                     job.ID,
					"pausedVia":                 "quota_threshold",
					"r5":                        quota.R5.UTC().Format(time.RFC3339),
					"outputTokensSinceSoftMark": 0,
					"ts":                        time.Now().UTC().Format(time.RFC3339),
				})
				_ = d.nc.Publish(lifecycleSubject, pausedPayload)

				// Update job status.
				job.Status = "paused"
				_ = d.writeJobEntry(job)
			}
		}

		// Check quota recovery: reset paused jobs when utilization is not exceeded
		// (i.e., when no jobs are threshold-exceeded with the current quota).
		if !anyExceeded {
			watcher2, err := d.jobQueueKV.WatchAll(ctx)
			if err == nil {
				var paused []*JobEntry
				for entry := range watcher2.Updates() {
					if entry == nil {
						break
					}
					var job JobEntry
					if json.Unmarshal(entry.Value(), &job) != nil {
						continue
					}
					if job.UserID == d.cfg.UserID && job.Status == "paused" {
						jobCopy := job
						paused = append(paused, &jobCopy)
					}
				}
				watcher2.Stop()

				// Sort by priority descending (highest first = first to restart).
				sort.Slice(paused, func(i, j int) bool {
					return paused[i].Priority > paused[j].Priority
				})
				for _, job := range paused {
					if job.ResumeAt != nil && job.ResumeAt.After(time.Now()) {
						continue // not ready yet (autoContinue with future reset time)
					}
					job.Status = "queued"
					job.ResumeAt = nil
					_ = d.writeJobEntry(job)
				}
			}
		}
	}

	// If the trigger was a newly queued job, try to start it.
	if changedJob != nil && changedJob.Status == "queued" {
		// Check quota allows starting (no threshold check required for non-hasData case).
		if !quota.HasData || quota.U5 < changedJob.Threshold {
			if err := d.dispatchQueuedJob(changedJob); err != nil {
				d.cfg.Log.Warn().Err(err).
					Str("jobId", changedJob.ID).
					Msg("daemon: job dispatch failed")
			}
		}
	}
}

// runJobsHTTP starts the jobs HTTP server on localhost:8378.
func (d *Daemon) runJobsHTTP(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/jobs", d.handleJobsRoute)
	mux.HandleFunc("/jobs/projects", d.handleJobsProjects)
	mux.HandleFunc("/jobs/", d.handleJobByID)

	srv := &http.Server{
		Addr:    jobsHTTPAddr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		d.cfg.Log.Warn().Err(err).Msg("daemon: jobs HTTP server error")
	}
}

// handleJobsRoute handles POST /jobs and GET /jobs.
func (d *Daemon) handleJobsRoute(w http.ResponseWriter, r *http.Request) {
	// The server is loopback-only; use the daemon's own userId from DaemonConfig.
	// No auth header required per spec (plan-quota-aware-scheduling.md §Daemon Jobs HTTP Server).
	userID := d.cfg.UserID

	switch r.Method {
	case http.MethodPost:
		var req struct {
			SpecPath     string `json:"specPath"`
			Priority     int    `json:"priority"`
			Threshold    int    `json:"threshold"`
			AutoContinue bool   `json:"autoContinue"`
			ProjectID    string `json:"projectId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Priority == 0 {
			req.Priority = 5
		}
		if req.Threshold == 0 {
			req.Threshold = 75
		}

		job := &JobEntry{
			ID:           uuid.NewString(),
			UserID:       userID,
			ProjectID:    req.ProjectID,
			SpecPath:     req.SpecPath,
			Priority:     req.Priority,
			Threshold:    req.Threshold,
			AutoContinue: req.AutoContinue,
			Status:       "queued",
			CreatedAt:    time.Now().UTC(),
		}
		if err := d.writeJobEntry(job); err != nil {
			http.Error(w, "KV write failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": job.ID, "status": "queued"})

	case http.MethodGet:
		watcher, err := d.jobQueueKV.WatchAll(context.Background())
		if err != nil {
			http.Error(w, "KV error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer watcher.Stop()

		var jobs []JobEntry
		for entry := range watcher.Updates() {
			if entry == nil {
				break
			}
			if entry.Operation() != jetstream.KeyValuePut {
				continue
			}
			var job JobEntry
			if json.Unmarshal(entry.Value(), &job) != nil {
				continue
			}
			if job.UserID == userID {
				jobs = append(jobs, job)
			}
		}
		// Sort by createdAt descending.
		sort.Slice(jobs, func(i, j int) bool {
			return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleJobByID handles GET /jobs/{id} and DELETE /jobs/{id}.
func (d *Daemon) handleJobByID(w http.ResponseWriter, r *http.Request) {
	// The server is loopback-only; use the daemon's own userId from DaemonConfig.
	// No auth header required per spec (plan-quota-aware-scheduling.md §Daemon Jobs HTTP Server).
	userID := d.cfg.UserID

	// Extract job ID from path: /jobs/{id}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/jobs/"), "/")
	jobID := parts[0]
	if jobID == "" || jobID == "projects" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		job, _, err := d.readJobEntry(userID, jobID)
		if err != nil {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(job)

	case http.MethodDelete:
		job, _, err := d.readJobEntry(userID, jobID)
		if err != nil {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		// If running, stop the session first.
		if job.Status == "running" && job.SessionID != "" {
			deleteSubject := subj.UserHostProjectAPISessionsDelete(d.cfg.UserSlug, d.cfg.HostSlug, jobProjectSlug(job))
			payload, _ := json.Marshal(map[string]string{"sessionId": job.SessionID})
			_ = d.nc.Publish(deleteSubject, payload)
		}
		job.Status = "cancelled"
		if err := d.writeJobEntry(job); err != nil {
			http.Error(w, "KV write failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Publish session_job_cancelled lifecycle event (spec: GAP-SA-K14).
		if job.SessionID != "" {
			lifecycleSubject := subj.UserHostProjectLifecycle(d.cfg.UserSlug, d.cfg.HostSlug, jobProjectSlug(job), slug.SessionSlug(job.SessionID))
			cancelPayload, _ := json.Marshal(map[string]string{
				"type":      "session_job_cancelled",
				"sessionId": job.SessionID,
				"jobId":     job.ID,
				"ts":        time.Now().UTC().Format(time.RFC3339),
			})
			_ = d.nc.Publish(lifecycleSubject, cancelPayload)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleJobsProjects handles GET /jobs/projects — returns projects from KV.
func (d *Daemon) handleJobsProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// The server is loopback-only; use the daemon's own userId from DaemonConfig.
	// No auth header required per spec (plan-quota-aware-scheduling.md §Daemon Jobs HTTP Server).
	userID := d.cfg.UserID

	watcher, err := d.projectsKV.WatchAll(context.Background())
	if err != nil {
		http.Error(w, "KV error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer watcher.Stop()

	type projectSummary struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var projects []projectSummary
	prefix := userID + "."

	for entry := range watcher.Updates() {
		if entry == nil {
			break
		}
		if entry.Operation() != jetstream.KeyValuePut {
			continue
		}
		if !strings.HasPrefix(entry.Key(), prefix) {
			continue
		}
		var ps ProjectState
		if json.Unmarshal(entry.Value(), &ps) != nil {
			continue
		}
		projects = append(projects, projectSummary{ID: ps.ID, Name: ps.Name})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projects)
}
