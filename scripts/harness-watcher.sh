#!/bin/bash
# harness-watcher.sh
# Watches /dev-harness sessions and retriggers if they go idle.
# Stops retriggering a component once the audit comes back clean.

COMPONENTS="session-agent control-plane cli spa helm"
WINDOWS="1 2 3 4 5"
TMUX_SESSION="mclaude"
API="https://localhost:8377"
POLL=15
COOLDOWN=60
RATE_LIMIT_COOLDOWN=300   # 5 min backoff after rate limit
CONTEXT_THRESHOLD=90      # don't retrigger if context usage >= this %
MAX_TRIGGERS=50
LOG="/tmp/harness-watcher.log"
STATE_DIR="/tmp/harness-watcher-state"

mkdir -p "$STATE_DIR"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >> "$LOG"; }

state_file() { echo "$STATE_DIR/$1.$2"; }

get_state() {
    local file
    file=$(state_file "$1" "$2")
    [ -f "$file" ] && cat "$file" || echo ""
}

set_state() {
    echo "$3" > "$(state_file "$1" "$2")"
}

get_status() {
    local window=$1
    curl -sk "$API/sessions" 2>/dev/null | python3 -c "
import json, sys, os
try:
    for s in json.load(sys.stdin):
        if s.get('tmuxSession') == 'mclaude' and s.get('tmuxWindow') == $window:
            print(s.get('status', 'unknown'))
            sys.stdout.flush()
            os._exit(0)
except Exception: pass
print('unknown')
" 2>/dev/null || echo "unknown"
}

is_done() {
    local window=$1
    tmux capture-pane -t "${TMUX_SESSION}:${window}" -p 2>/dev/null \
        | grep -qiE "0 missing|audit.*clean|all categories.*implemented|harness.*complete"
}

is_rate_limited() {
    local window=$1
    tmux capture-pane -t "${TMUX_SESSION}:${window}" -p 2>/dev/null \
        | grep -qiE "rate.?limit|429|too many requests|quota.*exceeded|slowdown|please.*wait.*before"
}

# Returns the context window usage % (e.g. "43" from "43% 200K"), or 0 if not found
get_context_pct() {
    local window=$1
    tmux capture-pane -t "${TMUX_SESSION}:${window}" -p 2>/dev/null \
        | grep -oE '[0-9]+% [0-9]+K' \
        | tail -1 \
        | grep -oE '^[0-9]+' \
        || echo "0"
}

# Returns the highest API rate-limit % visible in the status bar
# Format: "5h 93% ⇡26% 1h  7d 11%..." — grab all percentages after the "|" separator
get_api_rate_pct() {
    local window=$1
    tmux capture-pane -t "${TMUX_SESSION}:${window}" -p 2>/dev/null \
        | grep -oE '\|.*' \
        | grep -oE '[0-9]+%' \
        | grep -oE '^[0-9]+' \
        | sort -n \
        | tail -1 \
        || echo "0"
}

is_context_full() {
    local window=$1
    local ctx_pct api_pct max_pct
    ctx_pct=$(get_context_pct "$window")
    api_pct=$(get_api_rate_pct "$window")
    # Take the higher of context window vs API rate usage
    max_pct=$ctx_pct
    [ "$api_pct" -gt "$max_pct" ] 2>/dev/null && max_pct=$api_pct
    [ "$max_pct" -ge "$CONTEXT_THRESHOLD" ] 2>/dev/null
}

# For logging: returns "ctx=43% api=93%"
get_usage_summary() {
    local window=$1
    echo "ctx=$(get_context_pct "$window")% api=$(get_api_rate_pct "$window")%"
}

log "harness-watcher started (poll=${POLL}s cooldown=${COOLDOWN}s rate_limit_cooldown=${RATE_LIMIT_COOLDOWN}s max=${MAX_TRIGGERS})"

while true; do
    all_done=1
    idx=0
    poll_line=""
    for component in $COMPONENTS; do
        window=$(echo $WINDOWS | awk "{print \$$((idx+1))}")
        idx=$((idx + 1))

        # Skip if marked done
        if [ "$(get_state "$component" done)" = "1" ]; then
            poll_line="${poll_line} ${component}=done"
            continue
        fi

        all_done=0
        status=$(get_status "$window")
        prev=$(get_state "$component" status)
        now=$(date +%s)
        last_t=$(get_state "$component" trigger)
        [ -z "$last_t" ] && last_t=0
        count=$(get_state "$component" count)
        [ -z "$count" ] && count=0
        elapsed=$((now - last_t))

        # Detect audit-clean completion
        if [ "$status" = "idle" ] && is_done "$window"; then
            log "$component: audit clean — done, no more retriggers"
            set_state "$component" done "1"
            set_state "$component" status "$status"
            poll_line="${poll_line} ${component}=done"
            continue
        fi

        # Interrupt if context/rate is too full while working — stop before burning more tokens
        if [ "$status" = "working" ] && is_context_full "$window"; then
            # Only interrupt once per episode (don't spam Escape)
            if [ "$(get_state "$component" interrupted)" != "1" ]; then
                log "$component: $(get_usage_summary "$window") (>=${CONTEXT_THRESHOLD}%) — sending interrupt"
                tmux send-keys -t "${TMUX_SESSION}:${window}" Escape ""
                set_state "$component" interrupted "1"
            fi
            set_state "$component" status "$status"
            poll_line="${poll_line} ${component}=interrupted($(get_usage_summary "$window"))"
            continue
        fi

        # Retrigger: working → idle, cooldown elapsed, under cap
        if [ "$status" = "idle" ] && [ "$prev" = "working" ]; then
            # Clear interrupt flag so we can interrupt again next run if needed
            set_state "$component" interrupted "0"

            # Don't retrigger if context/rate is still too full after interrupt
            if is_context_full "$window"; then
                log "$component: $(get_usage_summary "$window") after interrupt — waiting for reset"
                set_state "$component" status "$status"
                poll_line="${poll_line} ${component}=waiting($(get_usage_summary "$window"))"
                continue
            fi

            # Pick cooldown based on whether rate-limited
            effective_cooldown=$COOLDOWN
            if is_rate_limited "$window"; then
                effective_cooldown=$RATE_LIMIT_COOLDOWN
                log "$component: rate limited — backing off ${RATE_LIMIT_COOLDOWN}s before retrigger"
            fi

            if [ "$elapsed" -ge "$effective_cooldown" ]; then
                if [ "$count" -ge "$MAX_TRIGGERS" ]; then
                    log "$component: hit max_triggers ($MAX_TRIGGERS), stopping"
                    set_state "$component" done "1"
                    poll_line="${poll_line} ${component}=capped"
                else
                    new_count=$((count + 1))
                    log "$component: idle after working (trigger #${new_count}) → /dev-harness $component"
                    tmux send-keys -t "${TMUX_SESSION}:${window}" "/dev-harness ${component}" Enter
                    set_state "$component" trigger "$now"
                    set_state "$component" count "$new_count"
                    poll_line="${poll_line} ${component}=retriggered(#${new_count})"
                fi
            else
                remaining=$((effective_cooldown - elapsed))
                poll_line="${poll_line} ${component}=cooldown(${remaining}s)"
            fi
        else
            poll_line="${poll_line} ${component}=${status}"
        fi

        [ "$status" != "unknown" ] && set_state "$component" status "$status"
    done

    log "poll:${poll_line}"

    if [ "$all_done" = "1" ]; then
        log "All components complete. Exiting."
        exit 0
    fi

    sleep "$POLL"
done
