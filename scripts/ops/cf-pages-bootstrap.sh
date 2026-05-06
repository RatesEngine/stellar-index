#!/usr/bin/env bash
# Idempotent Cloudflare Pages provisioning for ratesengine.net.
#
# Creates / updates three Pages projects (showcase, dashboard,
# status), binds custom domains, and (when the zone is on
# Cloudflare) writes the DNS records. Everything lives in CF
# config — no GUI clicks required after the one-time GitHub-app
# authorization at the CF account level (see PREREQ below).
#
# PREREQ: authorize Cloudflare's GitHub app on this org ONCE.
# The CF UI for that lives at:
#   https://dash.cloudflare.com/<account-id>/pages/new/connect
# Click "Connect GitHub", authorize the `RatesEngine` org,
# select the `rates-engine` repo. After that, this script can
# create new projects + change git config without ever opening
# the CF dashboard again.
#
# Usage:
#   export CLOUDFLARE_API_TOKEN=...   # Pages:Edit + Zone:Edit + DNS:Edit + Account:Read
#   export CLOUDFLARE_ACCOUNT_ID=...
#   bash scripts/ops/cf-pages-bootstrap.sh
#
# Optional env:
#   APEX_DOMAIN=ratesengine.net       # the zone the records live under
#   API_HOST=136.243.90.96            # the IP the api.<apex> A record points at
#   GITHUB_OWNER=RatesEngine
#   GITHUB_REPO=rates-engine
#   PRODUCTION_BRANCH=main
#   DRY_RUN=1                         # show what would change without touching CF
#
# Exit codes:
#   0 — every project + domain + DNS record is in the desired state
#   1 — usage / config error (missing token, bad token, etc.)
#   2 — CF API call failed (script aborted partway; safe to re-run)

set -euo pipefail

: "${CLOUDFLARE_API_TOKEN:?CLOUDFLARE_API_TOKEN env var is required}"
: "${CLOUDFLARE_ACCOUNT_ID:?CLOUDFLARE_ACCOUNT_ID env var is required}"

APEX_DOMAIN="${APEX_DOMAIN:-ratesengine.net}"
API_HOST="${API_HOST:-136.243.90.96}"
GITHUB_OWNER="${GITHUB_OWNER:-RatesEngine}"
GITHUB_REPO="${GITHUB_REPO:-rates-engine}"
PRODUCTION_BRANCH="${PRODUCTION_BRANCH:-main}"
DRY_RUN="${DRY_RUN:-0}"

CF_API="https://api.cloudflare.com/client/v4"

# ─── helpers ──────────────────────────────────────────────────────

log()  { printf '\033[1;34m▶\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31m✗\033[0m %s\n' "$*" >&2; }

# cf <method> <path> [body-json]  →  prints response body, exits non-zero on
# transport failure or `success: false` payload.
cf() {
  local method="$1" path="$2" body="${3:-}"
  local args=(-sS -X "$method"
              -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN"
              -H "Content-Type: application/json")
  if [[ -n "$body" ]]; then args+=(--data "$body"); fi
  local resp
  resp=$(curl "${args[@]}" "$CF_API$path") || {
    err "transport failure on $method $path"
    return 2
  }
  if ! jq -e '.success == true' >/dev/null <<<"$resp"; then
    err "CF API rejected $method $path:"
    jq '.errors // .messages // .' >&2 <<<"$resp"
    return 2
  fi
  printf '%s' "$resp"
}

# ─── token sanity + zone discovery ───────────────────────────────

log "Verifying API token + account access..."
cf GET "/user/tokens/verify" >/dev/null
ok "Token verifies"

log "Looking up zone for $APEX_DOMAIN..."
zone_resp=$(cf GET "/zones?name=$APEX_DOMAIN&account.id=$CLOUDFLARE_ACCOUNT_ID")
zone_id=$(jq -r '.result[0].id // empty' <<<"$zone_resp")
if [[ -z "$zone_id" ]]; then
  warn "$APEX_DOMAIN is not a Cloudflare zone in account $CLOUDFLARE_ACCOUNT_ID"
  warn "→ Pages projects + custom domains will still be created;"
  warn "  set DNS records manually at the registrar:"
  warn "    <project>.pages.dev → CNAME for app/status/root"
  warn "    $API_HOST → A for api.$APEX_DOMAIN"
else
  ok "Zone $APEX_DOMAIN id=$zone_id"
fi

# ─── pages projects ───────────────────────────────────────────────

# project_create_or_update <name> <root_dir> <build_command> <build_output_dir> [env_var=value ...]
project_create_or_update() {
  local name="$1" root_dir="$2" build_cmd="$3" out_dir="$4"
  shift 4
  local env_vars=("$@")

  local existing
  existing=$(cf GET "/accounts/$CLOUDFLARE_ACCOUNT_ID/pages/projects/$name" 2>/dev/null \
             | jq -r '.result.name // empty' || true)

  # Build the env-vars JSON object: { "KEY": { "value": "VAL", "type": "plain_text" }, ... }
  # Iterate via numeric index to avoid `set -u` tripping on an
  # empty array — `${env_vars[@]}` errors when the array has
  # zero elements; `${#env_vars[@]}` is always defined.
  local env_json='{}' i k v
  for (( i = 0; i < ${#env_vars[@]}; i++ )); do
    k="${env_vars[i]%%=*}"
    v="${env_vars[i]#*=}"
    env_json=$(jq --arg k "$k" --arg v "$v" \
                  '. + { ($k): { value: $v, type: "plain_text" } }' \
                  <<<"$env_json")
  done

  local body
  body=$(jq -n \
    --arg name "$name" \
    --arg branch "$PRODUCTION_BRANCH" \
    --arg owner "$GITHUB_OWNER" \
    --arg repo "$GITHUB_REPO" \
    --arg root "$root_dir" \
    --arg cmd "$build_cmd" \
    --arg out "$out_dir" \
    --argjson env "$env_json" \
    '{
       name: $name,
       production_branch: $branch,
       source: {
         type: "github",
         config: {
           owner: $owner,
           repo_name: $repo,
           production_branch: $branch,
           pr_comments_enabled: true,
           deployments_enabled: true,
           production_deployment_enabled: true,
           preview_deployment_setting: "all",
           preview_branch_includes: ["*"]
         }
       },
       build_config: {
         build_command: $cmd,
         destination_dir: $out,
         root_dir: $root
       },
       deployment_configs: {
         production: { env_vars: $env },
         preview:    { env_vars: $env }
       }
     }')

  if [[ -z "$existing" ]]; then
    log "[project] create $name (root=$root_dir)"
    if [[ "$DRY_RUN" == "1" ]]; then
      jq . <<<"$body"
      return
    fi
    cf POST "/accounts/$CLOUDFLARE_ACCOUNT_ID/pages/projects" "$body" >/dev/null
    ok "[project] $name created"
  else
    log "[project] update $name (root=$root_dir)"
    if [[ "$DRY_RUN" == "1" ]]; then
      jq . <<<"$body"
      return
    fi
    # Pages update is PATCH, body is the same shape.
    cf PATCH "/accounts/$CLOUDFLARE_ACCOUNT_ID/pages/projects/$name" "$body" >/dev/null
    ok "[project] $name updated"
  fi
}

# project_attach_domain <project> <domain>
project_attach_domain() {
  local project="$1" domain="$2"
  local existing
  existing=$(cf GET "/accounts/$CLOUDFLARE_ACCOUNT_ID/pages/projects/$project/domains" \
             | jq -r --arg d "$domain" '.result[] | select(.name == $d) | .name' || true)
  if [[ -n "$existing" ]]; then
    ok "[domain] $domain already on $project"
    return
  fi
  log "[domain] attach $domain to $project"
  if [[ "$DRY_RUN" == "1" ]]; then return; fi
  cf POST "/accounts/$CLOUDFLARE_ACCOUNT_ID/pages/projects/$project/domains" \
     "{\"name\":\"$domain\"}" >/dev/null
  ok "[domain] $domain attached to $project"
}

# dns_upsert_cname <name> <target>     (proxied=true)
# dns_upsert_a     <name> <ip>         (proxied=true)
dns_upsert() {
  local kind="$1" name="$2" content="$3"
  if [[ -z "$zone_id" ]]; then
    warn "[dns] skipped $name → $content (zone not on Cloudflare)"
    return
  fi
  local existing_id existing_content
  existing=$(cf GET "/zones/$zone_id/dns_records?type=$kind&name=$name")
  existing_id=$(jq -r '.result[0].id // empty' <<<"$existing")
  existing_content=$(jq -r '.result[0].content // empty' <<<"$existing")

  local body
  body=$(jq -n --arg t "$kind" --arg n "$name" --arg c "$content" \
            '{ type: $t, name: $n, content: $c, proxied: true, ttl: 1 }')

  if [[ -z "$existing_id" ]]; then
    log "[dns] create $kind $name → $content (proxied)"
    if [[ "$DRY_RUN" == "1" ]]; then return; fi
    cf POST "/zones/$zone_id/dns_records" "$body" >/dev/null
    ok "[dns] $name created"
  elif [[ "$existing_content" != "$content" ]]; then
    log "[dns] update $kind $name $existing_content → $content"
    if [[ "$DRY_RUN" == "1" ]]; then return; fi
    cf PUT "/zones/$zone_id/dns_records/$existing_id" "$body" >/dev/null
    ok "[dns] $name updated"
  else
    ok "[dns] $name already $content"
  fi
}

# ─── projects ─────────────────────────────────────────────────────

project_create_or_update \
  "ratesengine-showcase" \
  "web/showcase" \
  "pnpm install --frozen-lockfile && pnpm build" \
  "out" \
  "NEXT_PUBLIC_API_BASE_URL=https://api.$APEX_DOMAIN" \
  "NODE_VERSION=20" \
  "PNPM_VERSION=10"

project_create_or_update \
  "ratesengine-dashboard" \
  "web/dashboard" \
  "pnpm install --frozen-lockfile && pnpm build" \
  "out" \
  "NEXT_PUBLIC_API_BASE_URL=https://api.$APEX_DOMAIN" \
  "NODE_VERSION=20" \
  "PNPM_VERSION=10"

project_create_or_update \
  "ratesengine-status" \
  "deploy/status-page/cstate" \
  "hugo --minify" \
  "public" \
  "HUGO_VERSION=0.121.0"

# Static OpenAPI reference (Redocly-generated, committed to the
# repo). No build step — CF Pages just serves the directory.
# `make docs-api` regenerates the source HTML; CI checks the diff.
project_create_or_update \
  "ratesengine-docs" \
  "docs/reference/api" \
  "true" \
  "."

# ─── custom domains ──────────────────────────────────────────────

project_attach_domain "ratesengine-showcase"  "$APEX_DOMAIN"
project_attach_domain "ratesengine-showcase"  "www.$APEX_DOMAIN"
project_attach_domain "ratesengine-dashboard" "app.$APEX_DOMAIN"
project_attach_domain "ratesengine-status"    "status.$APEX_DOMAIN"
project_attach_domain "ratesengine-docs"      "docs.$APEX_DOMAIN"

# ─── DNS records (only if the zone is on Cloudflare) ─────────────

dns_upsert CNAME "$APEX_DOMAIN"        "ratesengine-showcase.pages.dev"
dns_upsert CNAME "www.$APEX_DOMAIN"    "ratesengine-showcase.pages.dev"
dns_upsert CNAME "app.$APEX_DOMAIN"    "ratesengine-dashboard.pages.dev"
dns_upsert CNAME "status.$APEX_DOMAIN" "ratesengine-status.pages.dev"
dns_upsert CNAME "docs.$APEX_DOMAIN"   "ratesengine-docs.pages.dev"
dns_upsert A     "api.$APEX_DOMAIN"    "$API_HOST"

# ─── done ─────────────────────────────────────────────────────────

ok "All done."
echo
echo "Verify:"
echo "  https://dash.cloudflare.com/$CLOUDFLARE_ACCOUNT_ID/pages"
echo "  https://$APEX_DOMAIN          (showcase)"
echo "  https://app.$APEX_DOMAIN      (dashboard)"
echo "  https://status.$APEX_DOMAIN   (status page)"
echo "  https://api.$APEX_DOMAIN/v1/healthz   (API behind CF proxy + r1 Caddy)"
