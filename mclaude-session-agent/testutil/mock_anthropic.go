package testutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// StartMockAnthropic starts an HTTP server that replays Anthropic API responses
// based on a transcript file. Session agent tests set ANTHROPIC_BASE_URL to
// the returned base URL, so the real Claude Code binary calls this server
// instead of api.anthropic.com.
//
// The mock converts stream-json events from the transcript into the Anthropic
// streaming SSE format (application/vnd.anthropic.v1+server-sent-events).
// Returns the base URL (e.g. "http://127.0.0.1:PORT"). Registers t.Cleanup.
func StartMockAnthropic(t *testing.T, transcriptPath string) string {
	t.Helper()

	turns := loadTranscriptForAnthropic(t, transcriptPath)
	idx := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if idx >= len(turns) {
			http.Error(w, "no more transcript turns", http.StatusGone)
			return
		}

		turn := turns[idx]
		idx++

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		for _, event := range turn {
			sseEvent := streamJSONToSSE(event)
			if sseEvent == "" {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", sseEvent)
			flusher.Flush()
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// loadTranscriptForAnthropic reads a transcript and splits it into turns,
// keeping only the events relevant to the Anthropic API response
// (stream_event and assistant events).
func loadTranscriptForAnthropic(t *testing.T, path string) [][]string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript %s: %v", path, err)
	}

	var allTurns [][]string
	var current []string

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		evType, _ := ev["type"].(string)
		switch evType {
		case "__turn_boundary__":
			allTurns = append(allTurns, current)
			current = nil
		case "stream_event", "assistant":
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		allTurns = append(allTurns, current)
	}

	// Filter out startup turns that have no API-relevant events.
	var turns [][]string
	for _, t := range allTurns {
		if len(t) > 0 {
			turns = append(turns, t)
		}
	}
	return turns
}

// streamJSONToSSE converts a stream-json event line to an Anthropic SSE data payload.
// Returns "" for events that have no SSE equivalent.
func streamJSONToSSE(line string) string {
	var ev map[string]any
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return ""
	}

	evType, _ := ev["type"].(string)
	switch evType {
	case "stream_event":
		// Unwrap: the inner event is the actual SSE payload.
		inner, _ := ev["event"].(map[string]any)
		if inner == nil {
			return ""
		}
		b, _ := json.Marshal(inner)
		return string(b)
	case "assistant":
		// Emit a message_start event followed by message_delta/stop.
		msg, _ := ev["message"].(map[string]any)
		if msg == nil {
			return ""
		}
		b, _ := json.Marshal(map[string]any{
			"type":    "message_stop",
			"message": msg,
		})
		return string(b)
	}
	return ""
}

// scanLines is a bufio scanner split function that handles both \n and \r\n.
var _ = bufio.ScanLines
