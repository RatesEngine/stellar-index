#!/usr/bin/env bash
#
# pre-launch-check.sh — verify R1 is in production-ready shape
# before the public DNS cutover at api.ratesengine.net.
#
# Read-only — performs zero state changes. Each check reports
# pass / warn / fail; exit code is the number of FAIL findings
# (warns don't gate). Designed to be safe to run on any R1
# whether or not it's already serving public traffic.
#
# Run on R1 itself:
#   ssh root@r1 'bash -s' < scripts/ops/pre-launch-check.sh
# Or interactively:
#   ssh root@r1 'bash /opt/ratesengine/pre-launch-check.sh'
#
# Companion to docs/operations/pre-launch-hardening.md — each
# check maps to a numbered step in that runbook.

set -uo pipefail

# ANSI colour helpers — disabled when stdout isn't a TTY.
if [ -t 1 ]; then
  GREEN="$(printf '\033[32m')"; YELLOW="$(printf '\033[33m')"; RED="$(printf '\033[31m')"; DIM="$(printf '\033[2m')"; OFF="$(printf '\033[0m')"
else
  GREEN=""; YELLOW=""; RED=""; DIM=""; OFF=""
fi

FAILS=0
WARNS=0

pass() {
  printf "  %sok%s   %-44s %s%s%s\n" "$GREEN" "$OFF" "$1" "$DIM" "$2" "$OFF"
}
warn() {
  printf "  %sWARN%s %-44s %s%s%s\n" "$YELLOW" "$OFF" "$1" "$DIM" "$2" "$OFF"
  WARNS=$((WARNS + 1))
}
fail() {
  printf "  %sFAIL%s %-44s %s%s%s\n" "$RED" "$OFF" "$1" "$DIM" "$2" "$OFF"
  FAILS=$((FAILS + 1))
}

CONFIG="${RATESENGINE_TOML:-/etc/ratesengine.toml}"
HC_ENV="${HEALTHCHECKS_ENV_FILE:-/etc/default/ratesengine-healthchecks}"
AM_ENV="${ALERTMANAGER_ENV_FILE:-/etc/default/alertmanager-secrets}"

echo "Pre-launch check — R1 $(hostname) — $(date -u +%FT%TZ)"
echo "Config: ${CONFIG}"
echo

# ── 1. API binds to loopback (or has trusted proxy CIDRs)
echo "  Network exposure"
if [ ! -f "$CONFIG" ]; then
  fail "config file present" "$CONFIG missing"
else
  listen_addr="$(grep -E '^\s*listen_addr\s*=' "$CONFIG" | head -1 | sed -E 's/.*=\s*"([^"]+)".*/\1/' || true)"
  if [ -z "$listen_addr" ]; then
    listen_addr="0.0.0.0:3000  (default)"
  fi
  case "$listen_addr" in
    127.0.0.1:*|localhost:*|"::1:"*)
      pass "listen_addr is loopback" "$listen_addr"
      ;;
    *)
      proxy_cidrs="$(grep -E '^\s*trusted_proxy_cidrs\s*=' "$CONFIG" | head -1 || true)"
      if [ -z "$proxy_cidrs" ] || echo "$proxy_cidrs" | grep -q '\[\s*\]'; then
        fail "listen_addr public + no trusted proxies" "$listen_addr"
      else
        warn "listen_addr public" "$listen_addr (proxy CIDRs configured)"
      fi
      ;;
  esac

  # Verify the running process matches.
  actual="$(ss -tlnp 2>/dev/null | awk '/ratesengine-api/ {print $4; exit}')"
  if [ -n "$actual" ]; then
    case "$actual" in
      127.0.0.1:*|"[::1]:"*)  pass "process bound to loopback" "$actual" ;;
      "*:"*|"0.0.0.0:"*|"[::]:"*)
        case "$listen_addr" in
          127.0.0.1:*|localhost:*|"::1:"*)
            warn "process bind != config" "$actual (config says $listen_addr; restart needed?)" ;;
          *)
            warn "process bound to all interfaces" "$actual" ;;
        esac
        ;;
      *)  pass "process bound" "$actual" ;;
    esac
  fi
fi
echo

# ── 2. CORS narrowed
echo "  CORS"
allowed_origins="$(grep -E '^\s*allowed_origins\s*=' "$CONFIG" 2>/dev/null | head -1 || true)"
if [ -z "$allowed_origins" ] || echo "$allowed_origins" | grep -q '"\*"'; then
  fail "allowed_origins is wide open" '["*"] — narrow to your showcase + API hostnames'
else
  pass "allowed_origins narrowed" "$(echo "$allowed_origins" | sed 's/^[[:space:]]*//')"
fi
echo

# ── 3. Stripe (only relevant if launching paid tiers)
echo "  Stripe"
if [ -f /etc/default/ratesengine ] && grep -q '^RATESENGINE_STRIPE_WEBHOOK_SECRET=whsec_' /etc/default/ratesengine 2>/dev/null; then
  pass "Stripe webhook secret set" "(whsec_… present)"
else
  warn "Stripe webhook secret not set" "ok if launching free-tier-only"
fi
echo

# ── 4. Healthchecks.io URLs
echo "  Healthchecks.io"
if [ ! -f "$HC_ENV" ]; then
  fail "HC env file missing" "$HC_ENV"
else
  for v in HEALTHCHECKS_URL_INDEXER HEALTHCHECKS_URL_AGGREGATOR HEALTHCHECKS_URL_API HEALTHCHECKS_URL_SMOKE; do
    if grep -q "^$v=https://" "$HC_ENV" 2>/dev/null; then
      pass "$v" "set"
    else
      fail "$v" "unset — see hardening doc step 6"
    fi
  done
fi
echo

# ── 5. Alertmanager secrets
echo "  Alertmanager"
if [ ! -f "$AM_ENV" ]; then
  fail "AM env file missing" "$AM_ENV"
else
  for v in HEALTHCHECKS_DEADMANSSWITCH_URL SLACK_WEBHOOK_URL; do
    if grep -q "^$v=https://" "$AM_ENV" 2>/dev/null; then
      pass "$v" "set"
    else
      warn "$v" "unset — alerts won't fan out"
    fi
  done
fi
echo

# ── 6. Timers active
echo "  Timers"
for t in 'ratesengine-heartbeat@indexer.timer' \
         'ratesengine-heartbeat@aggregator.timer' \
         'ratesengine-heartbeat@api.timer' \
         'ratesengine-smoke.timer'; do
  if systemctl is-active --quiet "$t" 2>/dev/null; then
    pass "$t" "active"
  else
    fail "$t" "inactive"
  fi
done
echo

# ── 7. Core services
echo "  Services"
for s in ratesengine-indexer.service \
         ratesengine-aggregator.service \
         ratesengine-api.service \
         caddy.service \
         prometheus.service \
         prometheus-alertmanager.service; do
  if systemctl is-active --quiet "$s" 2>/dev/null; then
    pass "$s" "active"
  else
    fail "$s" "inactive"
  fi
done
echo

# ── 8. Caddy serving on :443
echo "  Caddy"
if ss -tlnp 2>/dev/null | grep -q ':443.*caddy'; then
  pass "caddy listening on :443" ""
else
  fail "caddy not on :443" "TLS termination won't work"
fi
echo

# ── 9. API smoke
echo "  Smoke (loopback)"
if curl -fsS --max-time 5 http://localhost:3000/v1/healthz >/dev/null 2>&1; then
  pass "/v1/healthz" "200"
else
  fail "/v1/healthz" "loopback API not responding"
fi
if curl -fsS --max-time 5 http://localhost:3000/v1/status >/dev/null 2>&1; then
  pass "/v1/status" "200"
else
  fail "/v1/status" "not responding"
fi
echo

# ── 10. Boot warnings
echo "  Recent SECURITY warnings"
sec_warns="$(journalctl -u ratesengine-api -b -p warning --no-pager 2>/dev/null | grep -c SECURITY: || true)"
if [ "$sec_warns" -eq 0 ]; then
  pass "no SECURITY warnings since boot" ""
else
  fail "SECURITY warnings present" "$sec_warns lines — journalctl -u ratesengine-api -b -p warning | grep SECURITY"
fi
echo

# ── Summary
if [ "$FAILS" -eq 0 ] && [ "$WARNS" -eq 0 ]; then
  printf "%sAll checks passed — ready for DNS cutover.%s\n" "$GREEN" "$OFF"
elif [ "$FAILS" -eq 0 ]; then
  printf "%s%s warning(s); 0 failure(s) — review and proceed if intentional.%s\n" "$YELLOW" "$WARNS" "$OFF"
else
  printf "%s%s failure(s), %s warning(s) — fix before flipping DNS.%s\n" "$RED" "$FAILS" "$WARNS" "$OFF"
fi
echo
echo "Reference: docs/operations/pre-launch-hardening.md"

exit "$FAILS"
