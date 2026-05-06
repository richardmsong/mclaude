package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

// HostKVState is the value written to the mclaude-hosts JetStream KV bucket (ADR-0054).
// Key format: {hslug} (flat — hosts are globally unique).
// Must match the TypeScript HostKVState in mclaude-web.
// Per spec-state-schema.md: {slug, name, online, lastSeenAt} — no Type or Role field (ADR-0097).
type HostKVState struct {
	Slug       string  `json:"slug"`
	Name       string  `json:"name"`
	Online     bool    `json:"online"`
	LastSeenAt *string `json:"lastSeenAt,omitempty"`
}

// sysConnectEvent is the relevant subset of the $SYS.ACCOUNT.*.CONNECT event.
type sysConnectEvent struct {
	Server struct {
		Name string `json:"name"`
	} `json:"server"`
	Client struct {
		Kind  string `json:"kind"`  // "Client" or "Leafnode"
		Name  string `json:"name"`
		NKey  string `json:"nkey"`  // NKey public key of the connecting client
	} `json:"client"`
}

// StartSysSubscriber subscribes to $SYS.ACCOUNT.*.CONNECT and
// $SYS.ACCOUNT.*.DISCONNECT on hub NATS. Per ADR-0054/ADR-0063/ADR-0097:
//   - kind="Client" + nkey matches hosts.public_key → update last_seen_at and
//     set online=true in KV (ADR-0097: no type column; all hosts are K8s user-agent hosts)
//   - kind="Leafnode" → log warning and drop (leaf topology removed per ADR-0054)
//   - No match → ignore (SPA ephemeral NKey, control-plane's own connection)
//
// DISCONNECT mirrors with online=false (does not rewrite last_seen_at).
func (s *Server) StartSysSubscriber(nc *nats.Conn) error {
	// Subscribe to CONNECT events.
	if _, err := nc.Subscribe("$SYS.ACCOUNT.*.CONNECT", func(msg *nats.Msg) {
		s.handleSysEvent(msg, true)
	}); err != nil {
		return err
	}

	// Subscribe to DISCONNECT events.
	if _, err := nc.Subscribe("$SYS.ACCOUNT.*.DISCONNECT", func(msg *nats.Msg) {
		s.handleSysEvent(msg, false)
	}); err != nil {
		return err
	}

	return nil
}

// handleSysEvent processes a $SYS.ACCOUNT.*.CONNECT or DISCONNECT event.
func (s *Server) handleSysEvent(msg *nats.Msg, isConnect bool) {
	if s.db == nil {
		return
	}

	var evt sysConnectEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		log.Debug().Err(err).Msg("$SYS event: unmarshal failed")
		return
	}

	nkeyPub := evt.Client.NKey
	if nkeyPub == "" {
		return
	}

	ctx := context.Background()

	switch evt.Client.Kind {
	case "Client":
		// Look up by public_key only — no type filter (ADR-0097: type column removed).
		// ADR-0063: K8s cluster controllers connect hub-direct as "Client", not "Leafnode".
		if isConnect {
			now := time.Now().UTC()
			_, err := s.db.Pool().Exec(ctx, `
				UPDATE hosts SET last_seen_at = $1
				WHERE public_key = $2`,
				now, nkeyPub)
			if err != nil {
				log.Warn().Err(err).Str("nkey", nkeyPub).Msg("$SYS CONNECT: update host last_seen_at")
			}

			// KV write: {hslug} → online=true (ADR-0054: flat key, no user prefix).
			// ADR-0097: type column removed — query only slug and name.
			if s.hostsKV != nil {
				var hslug, hname string
				qerr := s.db.Pool().QueryRow(ctx, `
					SELECT h.slug AS hslug, h.name
					FROM hosts h
					WHERE h.public_key = $1`,
					nkeyPub).Scan(&hslug, &hname)
				if qerr == nil {
					nowStr := now.Format(time.RFC3339)
					state := HostKVState{
						Slug:       hslug,
						Name:       hname,
						Online:     true,
						LastSeenAt: &nowStr,
					}
					if val, merr := json.Marshal(state); merr == nil {
						key := hslug
						if _, perr := s.hostsKV.Put(key, val); perr != nil {
							log.Warn().Err(perr).Str("key", key).Msg("$SYS CONNECT: hostsKV put failed")
						}
					}
				}
			}
		} else {
			// DISCONNECT: no last_seen_at update per ADR-0035; set online=false in KV.
			// ADR-0097: type column removed — query only slug and name.
			if s.hostsKV != nil {
				var hslug, hname string
				qerr := s.db.Pool().QueryRow(ctx, `
					SELECT h.slug AS hslug, h.name
					FROM hosts h
					WHERE h.public_key = $1`,
					nkeyPub).Scan(&hslug, &hname)
				if qerr == nil {
					key := hslug
					// Read-modify-write: preserve lastSeenAt from existing entry.
					// If no existing entry, skip the write.
					existing, gerr := s.hostsKV.Get(key)
					if gerr == nil {
						var state HostKVState
						if jerr := json.Unmarshal(existing.Value(), &state); jerr != nil {
							// Fallback: initialize from DB values (type/role intentionally omitted per ADR-0097).
							state = HostKVState{Slug: hslug, Name: hname}
						}
						state.Online = false
						if val, merr := json.Marshal(state); merr == nil {
							if _, perr := s.hostsKV.Put(key, val); perr != nil {
								log.Warn().Err(perr).Str("key", key).Msg("$SYS DISCONNECT: hostsKV put failed")
							}
						}
					}
				}
			}
		}

	default:
		// Unexpected event kind — leaf topology was removed in ADR-0054/ADR-0063.
		// Log a warning and drop.
		log.Warn().Str("kind", evt.Client.Kind).Str("nkey", nkeyPub).
			Msg("$SYS event: unexpected client kind; leaf topology removed per ADR-0063 — dropping")
	}
}
