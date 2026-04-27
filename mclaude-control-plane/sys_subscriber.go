package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

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
		}
		// DISCONNECT: no last_seen_at update per ADR-0035.

	case "Leafnode":
		// Cluster host: look up by public_key + type='cluster', update ALL rows
		// with matching slug (cluster-shared liveness across granted users).
		var slug string
		err := s.db.pool.QueryRow(ctx, `
			SELECT slug FROM hosts
			WHERE public_key = $1 AND type = 'cluster' LIMIT 1`,
			nkeyPub).Scan(&slug)
		if err != nil {
			// No match — ignore (could be a non-mclaude leaf node).
			return
		}
		if isConnect {
			now := time.Now().UTC()
			_, err = s.db.pool.Exec(ctx, `
				UPDATE hosts SET last_seen_at = $1
				WHERE slug = $2 AND type = 'cluster'`,
				now, slug)
			if err != nil {
				log.Warn().Err(err).Str("slug", slug).Msg("$SYS CONNECT: update cluster host last_seen_at")
			}
		}
		// DISCONNECT: no last_seen_at update per ADR-0035.
	}
}
