// mock-claude is a test binary that speaks the stream-json protocol over
// stdin/stdout.  It replays a canned transcript instead of calling Claude.
//
// Usage:
//
//	MOCK_TRANSCRIPT=/path/to/transcript.jsonl mock-claude [claude flags ignored]
//
// Transcript format: NDJSON file, one stream-json event per line.
// Turns are separated by {"type":"__turn_boundary__"}.
// A {"type":"__crash__"} line causes mock-claude to exit(1) immediately
// (used by crash_mid_tool tests).
//
// Behaviour:
//
//  1. Emit the first turn (startup events: init, session_state_changed idle)
//     immediately on startup.
//  2. For each subsequent turn: read one line from stdin, then emit the turn.
//  3. After the last turn, exit 0.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	transcriptPath := os.Getenv("MOCK_TRANSCRIPT")
	if transcriptPath == "" {
		fmt.Fprintln(os.Stderr, "mock-claude: MOCK_TRANSCRIPT not set")
		os.Exit(1)
	}

	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mock-claude: read transcript: %v\n", err)
		os.Exit(1)
	}

	turns := parseTurns(strings.TrimSpace(string(data)))

	if len(turns) == 0 {
		os.Exit(0)
	}

	// Emit startup turn immediately.
	if err := emitTurn(turns[0]); err != nil {
		os.Exit(1)
	}

	// For each remaining turn, wait for one line of stdin then emit.
	stdinScanner := bufio.NewScanner(os.Stdin)
	for _, turn := range turns[1:] {
		if !stdinScanner.Scan() {
			// stdin closed — no more input.
			break
		}
		if err := emitTurn(turn); err != nil {
			os.Exit(1)
		}
	}

	os.Exit(0)
}

// parseTurns splits the transcript into turns at __turn_boundary__ lines.
func parseTurns(content string) [][]string {
	var turns [][]string
	var current []string

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// Skip malformed lines silently.
			continue
		}

		if evType, _ := ev["type"].(string); evType == "__turn_boundary__" {
			turns = append(turns, current)
			current = nil
			continue
		}

		current = append(current, line)
	}

	if len(current) > 0 {
		turns = append(turns, current)
	}

	return turns
}

// emitTurn writes all lines in a turn to stdout, watching for __crash__.
func emitTurn(lines []string) error {
	stdout := bufio.NewWriter(os.Stdout)
	for _, line := range lines {
		var ev map[string]any
		_ = json.Unmarshal([]byte(line), &ev)
		if evType, _ := ev["type"].(string); evType == "__crash__" {
			// Simulate crash mid-turn.
			_ = stdout.Flush()
			os.Exit(1)
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return stdout.Flush()
}
