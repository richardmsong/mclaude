#!/usr/bin/env node

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

const MCLAUDE_URL = process.env.MCLAUDE_URL ?? "http://localhost:8377";

async function api(path: string, options?: RequestInit): Promise<unknown> {
  const res = await fetch(`${MCLAUDE_URL}${path}`, options);
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`MClaude API ${res.status}: ${body}`);
  }
  const text = await res.text();
  return text ? JSON.parse(text) : null;
}

const server = new McpServer({
  name: "mclaude",
  version: "1.0.0",
});

// List all active Claude Code sessions
server.tool("list_sessions", "List all active Claude Code sessions with their status and working directory", {}, async () => {
  const sessions = await api("/sessions");
  return { content: [{ type: "text", text: JSON.stringify(sessions, null, 2) }] };
});

// Get details for a specific session
server.tool(
  "get_session",
  "Get details for a specific session including its current status and detected prompt",
  { id: z.string().describe("Session ID (tmux window index)") },
  async ({ id }) => {
    const session = await api(`/sessions/${id}`);
    return { content: [{ type: "text", text: JSON.stringify(session, null, 2) }] };
  }
);

// Get terminal output from a session
server.tool(
  "get_session_output",
  "Get the current terminal output from a Claude Code session",
  { id: z.string().describe("Session ID (tmux window index)") },
  async ({ id }) => {
    const result = await api(`/sessions/${id}/output`) as { output: string };
    return { content: [{ type: "text", text: result.output }] };
  }
);

// Get structured events from a session
server.tool(
  "get_session_events",
  "Get recent structured events (tool use, text, thinking) from a session's JSONL log",
  {
    id: z.string().describe("Session ID (tmux window index)"),
  },
  async ({ id }) => {
    const events = await api(`/sessions/${id}/events`);
    return { content: [{ type: "text", text: JSON.stringify(events, null, 2) }] };
  }
);

// List available project directories
server.tool("list_projects", "List available project directories in ~/work/", {}, async () => {
  const projects = await api("/projects");
  return { content: [{ type: "text", text: JSON.stringify(projects, null, 2) }] };
});

// Create a new Claude Code session
server.tool(
  "create_session",
  "Create a new Claude Code session in a tmux window with a specified working directory. Optionally send an initial prompt.",
  {
    cwd: z.string().describe("Working directory for the new session (absolute path)"),
    prompt: z.string().optional().describe("Initial prompt to send to the session after creation"),
  },
  async ({ cwd, prompt }) => {
    // Snapshot existing session IDs before creation
    const existingSessions = (await api("/sessions")) as Array<{ id: string }>;
    const existingIds = new Set(existingSessions.map((s) => s.id));

    await api("/sessions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ cwd }),
    });

    if (prompt) {
      // Wait for the NEW session to appear and become idle
      let sessionId: string | null = null;
      for (let i = 0; i < 20; i++) {
        await new Promise((r) => setTimeout(r, 1000));
        const sessions = (await api("/sessions")) as Array<{ id: string; cwd: string; status: string }>;
        const match = sessions.find((s) => !existingIds.has(s.id) && s.status === "idle");
        if (match) {
          sessionId = match.id;
          break;
        }
      }

      if (sessionId) {
        // Extra delay to ensure Claude Code's input field is fully ready
        await new Promise((r) => setTimeout(r, 2000));
        await api(`/sessions/${sessionId}/input`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ text: prompt }),
        });
        return {
          content: [{ type: "text", text: `Session ${sessionId} created in ${cwd} and prompt sent.` }],
        };
      } else {
        return {
          content: [{ type: "text", text: `Session created in ${cwd} but timed out waiting for idle state to send prompt.` }],
        };
      }
    }

    return { content: [{ type: "text", text: `Session created in ${cwd}.` }] };
  }
);

// Send input to a session
server.tool(
  "send_input",
  "Send text input to an active Claude Code session",
  {
    id: z.string().describe("Session ID (tmux window index)"),
    text: z.string().describe("Text to send to the session"),
  },
  async ({ id, text }) => {
    await api(`/sessions/${id}/input`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    });
    return { content: [{ type: "text", text: `Input sent to session ${id}.` }] };
  }
);

// Approve a permission prompt
server.tool(
  "approve_session",
  "Approve a pending permission prompt in a Claude Code session (sends Enter)",
  { id: z.string().describe("Session ID (tmux window index)") },
  async ({ id }) => {
    await api(`/sessions/${id}/approve`, { method: "POST" });
    return { content: [{ type: "text", text: `Approved permission in session ${id}.` }] };
  }
);

// Cancel an operation
server.tool(
  "cancel_session",
  "Cancel the current operation in a Claude Code session (sends Escape)",
  { id: z.string().describe("Session ID (tmux window index)") },
  async ({ id }) => {
    await api(`/sessions/${id}/cancel`, { method: "POST" });
    return { content: [{ type: "text", text: `Cancelled operation in session ${id}.` }] };
  }
);

async function main() {
  const transport = new StdioServerTransport();
  await server.connect(transport);
}

main().catch((err) => {
  console.error("Fatal:", err);
  process.exit(1);
});
