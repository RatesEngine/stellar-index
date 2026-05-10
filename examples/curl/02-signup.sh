#!/usr/bin/env bash
# POST /v1/signup — self-service first-key issuance.
#
# Returns a freshly minted Starter-tier API key. The plaintext is
# returned exactly once — store it. Idempotent on email: a second
# signup with the same address replays the original key_id but
# does NOT re-mint the plaintext (clients that lost the original
# response need a fresh email or operator help).
#
# Production-safety: the default `demo+<timestamp>@example.com`
# email + the public default endpoint mean a casual `bash
# 02-signup.sh` would create a real account row in prod every
# time. The CONFIRM_PROD_SIGNUP=1 gate forces explicit
# acknowledgement. Local / staging deployments (any non-prod
# `API_BASE_URL`) bypass the gate.
set -euo pipefail
BASE="${API_BASE_URL:-https://api.ratesengine.net}"
EMAIL="${1:-demo+$(date +%s)@example.com}"
LABEL="${2:-curl-example}"

if [[ "$BASE" == *"api.ratesengine.net"* ]] && [[ "${CONFIRM_PROD_SIGNUP:-}" != "1" ]]; then
  cat >&2 <<EOF
Refusing to POST to production without confirmation.

This would create a real account row at $BASE with email
'$EMAIL' and label '$LABEL' — a side-effect that's not what
most "smoke-test the example" runs intend.

If you actually want to sign up against production, run:
  CONFIRM_PROD_SIGNUP=1 bash $0 [email] [label]

Or override the endpoint to your local / staging deployment:
  API_BASE_URL=http://localhost:3000 bash $0
EOF
  exit 1
fi

curl -sS --fail -X POST "$BASE/v1/signup" \
  -H 'Content-Type: application/json' \
  -d "{\"email\": \"$EMAIL\", \"label\": \"$LABEL\"}"
echo
