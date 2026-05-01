package drivers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ClaudeCodeDriver implements CLIDriver for the Claude Code CLI backend.
// It spawns `claude --print --verbose --output-format stream-json` and
// translates its NDJSON stdout into DriverEvents.
type ClaudeCodeDriver struct {
	claudePath string
}

// NewClaudeCodeDriver creates a ClaudeCodeDriver using the given claude binary path.
// If claudePath is empty, defaults to "claude" (resolved via PATH).
func NewClaudeCodeDriver(claudePath string) *ClaudeCodeDriver {
	if claudePath == "" {
		claudePath = "claude"
	}
	return &ClaudeCodeDriver{claudePath: claudePath}
}

func (d *ClaudeCodeDriver) Backend() CLIBackend { return BackendClaudeCode }

func (d *ClaudeCodeDriver) DisplayName() string { return "Claude Code" }

func (d *ClaudeCodeDriver) Capabilities() CLICapabilities {
	return CLICapabilities{
		HasThinking:      true,
		HasSubagents:     true,
		HasSkills:        true,
		HasPlanMode:      false,
		HasMissions:      false,
		HasEventStream:   true,
		HasSessionResume: true,
		ThinkingLabel:    "Thinking",
		PermissionModes:  []string{"auto", "managed", "allowlist", "strict-allowlist"},
	}
}

// buildArgs constructs the claude command-line arguments for a new or resumed session.
func (d *ClaudeCodeDriver) buildArgs(sessionID string, resume bool, opts LaunchOptions) ([]string, error) {
	var args []string
	if resume {
		args = []string{
			"--print", "--verbose",
			"--output-format", "stream-json",
			"--input-format", "stream-json",
			"--include-partial-messages",
			"--replay-user-messages",
			"--resume", sessionID,
		}
	} else {
		args = []string{
			"--print", "--verbose",
			"--output-format", "stream-json",
			"--input-format", "stream-json",
			"--include-partial-messages",
			"--replay-user-messages",
			"--session-id", sessionID,
		}
	}

	// Append shell-parsed extra flags if set.
	if opts.ExtraFlags != "" {
		extra, err := shellSplit(opts.ExtraFlags)
		if err != nil {
			return nil, fmt.Errorf("extraFlags shell parse: %w", err)
		}
		args = append(args, extra...)
	}

	return args, nil
}

// Launch spawns a new Claude Code process for the given session.
func (d *ClaudeCodeDriver) Launch(ctx context.Context, opts LaunchOptions) (*Process, error) {
	return d.spawn(ctx, opts.SessionID, false, opts)
}

// Resume spawns a Claude Code process in resume mode (--resume <sessionID>).
func (d *ClaudeCodeDriver) Resume(ctx context.Context, sessionID string, opts LaunchOptions) (*Process, error) {
	return d.spawn(ctx, sessionID, true, opts)
}

// spawn creates and starts a Claude Code process.
func (d *ClaudeCodeDriver) spawn(ctx context.Context, sessionID string, resume bool, opts LaunchOptions) (*Process, error) {
	args, err := d.buildArgs(sessionID, resume, opts)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, d.claudePath, args...)
	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}
	if len(opts.ExtraEnv) > 0 {
		cmd.Env = append(append([]string{}, os.Environ()...), opts.ExtraEnv...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	return &Process{
		Cmd:    cmd,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		PID:    cmd.Process.Pid,
	}, nil
}

// SendMessage writes a user message to Claude Code's stdin in stream-json format.
// Format: {"type":"user","message":{"role":"user","content":"<text>"}}
func (d *ClaudeCodeDriver) SendMessage(proc *Process, msg UserMessage) error {
	// Build the content field: string for text-only, array for attachments.
	var content interface{}
	if len(msg.Attachments) == 0 {
		content = msg.Text
	} else {
		// Content array with text block + image blocks.
		blocks := []map[string]interface{}{
			{"type": "text", "text": msg.Text},
		}
		for _, att := range msg.Attachments {
			block := map[string]interface{}{
				"type":       "image",
				"media_type": att.MimeType,
				"data":       att.Data,
			}
			blocks = append(blocks, block)
		}
		content = blocks
	}

	payload, err := json.Marshal(map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": content,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal user message: %w", err)
	}

	return d.writeLine(proc.Stdin, payload)
}

// SendPermissionResponse writes a control_response to Claude Code's stdin.
// allow=true → behavior:"allow", allow=false → behavior:"deny".
func (d *ClaudeCodeDriver) SendPermissionResponse(proc *Process, requestID string, allow bool) error {
	behavior := "allow"
	if !allow {
		behavior = "deny"
	}
	payload, err := json.Marshal(map[string]interface{}{
		"type": "control_response",
		"response": map[string]interface{}{
			"subtype":    "success",
			"request_id": requestID,
			"response":   map[string]string{"behavior": behavior},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal permission response: %w", err)
	}
	return d.writeLine(proc.Stdin, payload)
}

// UpdateConfig is a no-op for ClaudeCodeDriver — Claude Code config is set at
// launch time via flags. Dynamic config updates are not supported.
func (d *ClaudeCodeDriver) UpdateConfig(_ *Process, _ SessionConfig) error {
	return nil
}

// Interrupt sends a control_request interrupt to Claude Code's stdin.
// This signals Claude to abort its current operation.
func (d *ClaudeCodeDriver) Interrupt(proc *Process) error {
	interrupt := []byte(`{"type":"control_request","request":{"subtype":"interrupt"}}`)
	return d.writeLine(proc.Stdin, interrupt)
}

// ReadEvents reads stream-json NDJSON lines from proc.Stdout, wraps each line in
// a DriverEvent, and sends it on the out channel. Blocks until stdout is closed
// (process exits). The NativeType field of each DriverEvent contains the stream-json
// "type" field value. Raw contains the original bytes.
//
// This method is run in a goroutine by the session agent.
func (d *ClaudeCodeDriver) ReadEvents(proc *Process, out chan<- DriverEvent) error {
	scanner := bufio.NewScanner(proc.Stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		// Make a copy since scanner reuses the buffer.
		raw := make([]byte, len(line))
		copy(raw, line)

		// Extract the native event type from the "type" field.
		nativeType := extractJSONStringField(raw, "type")

		out <- DriverEvent{
			NativeType: nativeType,
			Raw:        raw,
		}
	}

	return scanner.Err()
}

// writeLine writes a JSON payload followed by a newline to the given writer.
func (d *ClaudeCodeDriver) writeLine(w io.Writer, payload []byte) error {
	_, err := w.Write(append(payload, '\n'))
	return err
}

// extractJSONStringField extracts the value of a top-level string field from JSON
// without full unmarshal (fast path for the "type" field lookup).
func extractJSONStringField(data []byte, field string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}
	raw, ok := obj[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// shellSplit performs a POSIX-style shell split of a string of flags.
// This is the same as the main package's shellSplit function; duplicated
// here to avoid a circular dependency.
func shellSplit(s string) ([]string, error) {
	var words []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDoubleQuote:
			inSingleQuote = !inSingleQuote
		case c == '"' && !inSingleQuote:
			inDoubleQuote = !inDoubleQuote
		case c == '\\' && !inSingleQuote:
			if i+1 < len(s) {
				i++
				current.WriteByte(s[i])
			}
		case (c == ' ' || c == '\t' || c == '\n') && !inSingleQuote && !inDoubleQuote:
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	if inSingleQuote {
		return nil, fmt.Errorf("unterminated single quote")
	}
	if inDoubleQuote {
		return nil, fmt.Errorf("unterminated double quote")
	}
	return words, nil
}
