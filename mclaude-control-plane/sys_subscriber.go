package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

// HostKVState is the value written to the mclaude-hosts JetStream KV bucket (ADR-0046).
// Key format: {uslug}.{hslug}
// Must match the TypeScript HostKVState in mclaude-web.
type HostKVState struct {
	Slug       string  `json:"slug"`
	Type       string  `json:"type"`
	Name       string  `json:"name"`
	Role       string  `json:"role"`
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
// $SYS.ACCOUNT.*.DISCONNECT on hub NATS. Per ADR-0035:
//   - kind="Client" + nkey matches hosts.public_key with type='machine'
//     → update that row's last_seen_at and set online in KV
//   - kind="Leafnode" + nkey matches a row with type='cluster'
//     → update ALL rows where slug=found.slug AND type='cluster'
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
		// Machine host: look up by public_key + type='machine'.
		if isConnect {
			now := time.Now().UTC()
			_, err := s.db.pool.Exec(ctx, `
				UPDATE hosts SET last_seen_at = $1
				WHERE public_key = $2 AND type = 'machine'`,
				now, nkeyPub)
			if err != nil {
				log.Warn().Err(err).Str("nkey", nkeyPub).Msg("$SYS CONNECT: update machine host last_seen_at")
			}

			// KV write: {uslug}.{hslug} → online=true (ADR-0046).
			if s.hostsKV != nil {
				var hslug, hname, htype, hrole, uslug string
				qerr := s.db.pool.QueryRow(ctx, `
					SELECT h.slug AS hslug, h.name, h.type, h.role, u.slug AS uslug
					FROM hosts h JOIN users u ON h.user_id = u.id
					WHERE h.public_key = $1 AND h.type = 'machine'`,
					nkeyPub).Scan(&hslug, &hname, &htype, &hrole, &uslug)
				if qerr == nil {
					nowStr := now.Format(time.RFC3339)
					state := HostKVState{
						Slug:       hslug,
						Type:       htype,
						Name:       hname,
						Role:       hrole,
						Online:     true,
						LastSeenAt: &nowStr,
					}
					if val, merr := json.Marshal(state); merr == nil {
						key := fmt.Sprintf("%s.%s", uslug, hslug)
						if _, perr := s.hostsKV.Put(key, val); perr != nil {
							log.Warn().Err(perr).Str("key", key).Msg("$SYS CONNECT: hostsKV put failed")
						}
					}
				}
			}
		} else {
			// DISCONNECT: no last_seen_at update per ADR-0035; set online=false in KV.
			if s.hostsKV != nil {
				var hslug, hname, htype, hrole, uslug string
				qerr := s.db.pool.QueryRow(ctx, `
					SELECT h.slug AS hslug, h.name, h.type, h.role, u.slug AS uslug
					FROM hosts h JOIN users u ON h.user_id = u.id
					WHERE h.public_key = $1 AND h.type = 'machine'`,
					nkeyPub).Scan(&hslug, &hname, &htype, &hrole, &uslug)
				if qerr == nil {
					key := fmt.Sprintf("%s.%s", uslug, hslug)
					// Read-modify-write: preserve lastSeenAt from existing entry.
					// If no existing entry, skip the write.
					existing, gerr := s.hostsKV.Get(key)
					if gerr == nil {
						var state HostKVState
						if jerr := json.Unmarshal(existing.Value(), &state); jerr != nil {
							state = HostKVState{Slug: hslug, Type: htype, Name: hname, Role: hrole}
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

	case "Leafnode":
		// Cluster host: look up by public_key + type='cluster', update ALL rows
		// with matching slug (cluster-shared liveness across granted users).
		var clusterSlug string
		err := s.db.pool.QueryRow(ctx, `
			SELECT slug FROM hosts
			WHERE public_key = $1 AND type = 'cluster' LIMIT 1`,
			nkeyPub).Scan(&clusterSlug)
		if err != nil {
			// No match — ignore (could be a non-mclaude leaf node).
			return
		}
		if isConnect {
			now := time.Now().UTC()
			_, err = s.db.pool.Exec(ctx, `
				UPDATE hosts SET last_seen_at = $1
				WHERE slug = $2 AND type = 'cluster'`,
				now, clusterSlug)
			if err != nil {
				log.Warn().Err(err).Str("slug", clusterSlug).Msg("$SYS CONNECT: update cluster host last_seen_at")
			}

			// KV write: {uslug}.{hslug} → online=true for each user row (ADR-0046).
			if s.hostsKV != nil {
				rows, qerr := s.db.pool.Query(ctx, `
					SELECT u.slug AS uslug, h.slug AS hslug, h.name, h.type, h.role
					FROM hosts h JOIN users u ON h.user_id = u.id
					WHERE h.slug = $1 AND h.type = 'cluster'`,
					clusterSlug)
				if qerr == nil {
					defer rows.Close()
					nowStr := now.Format(time.RFC3339)
					for rows.Next() {
						var uslug, hslug, hname, htype, hrole string
						if serr := rows.Scan(&uslug, &hslug, &hname, &htype, &hrole); serr != nil {
							continue
						}
						state := HostKVState{
							Slug:       hslug,
							Type:       htype,
							Name:       hname,
							Role:       hrole,
							Online:     true,
							LastSeenAt: &nowStr,
						}
						if val, merr := json.Marshal(state); merr == nil {
							key := fmt.Sprintf("%s.%s", uslug, hslug)
							if _, perr := s.hostsKV.Put(key, val); perr != nil {
								log.Warn().Err(perr).Str("key", key).Msg("$SYS CONNECT: hostsKV cluster put failed")
							}
						}
					}
				}
			}
		} else {
			// DISCONNECT: no last_seen_at update per ADR-0035; set online=false in KV.
			if s.hostsKV != nil {
				rows, qerr := s.db.pool.Query(ctx, `
					SELECT u.slug AS uslug, h.slug AS hslug, h.name, h.type, h.role
					FROM hosts h JOIN users u ON h.user_id = u.id
					WHERE h.slug = $1 AND h.type = 'cluster'`,
					clusterSlug)
				if qerr == nil {
					defer rows.Close()
					for rows.Next() {
						var uslug, hslug, hname, htype, hrole string
						if serr := rows.Scan(&uslug, &hslug, &hname, &htype, &hrole); serr != nil {
							continue
						}
						key := fmt.Sprintf("%s.%s", uslug, hslug)
						// Read-modify-write: preserve lastSeenAt from existing entry.
						existing, gerr := s.hostsKV.Get(key)
						if gerr != nil {
							// No existing entry — skip.
							continue
						}
						var state HostKVState
						if jerr := json.Unmarshal(existing.Value(), &state); jerr != nil {
							state = HostKVState{Slug: hslug, Type: htype, Name: hname, Role: hrole}
						}
						state.Online = false
						if val, merr := json.Marshal(state); merr == nil {
							if _, perr := s.hostsKV.Put(key, val); perr != nil {
								log.Warn().Err(perr).Str("key", key).Msg("$SYS DISCONNECT: hostsKV cluster put failed")
							}
						}
					}
				}
			}
		}
	}
}
