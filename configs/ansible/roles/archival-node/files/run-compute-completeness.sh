#!/usr/bin/env bash
# ADR-0033 completeness-verdict refresh — self-chunking driver.
#
# compute-completeness.service runs this on a daily timer. For EACH source it
# walks [its current watermark, ledgerstream tip] in CHUNK-ledger windows and
# re-verifies via `stellarindex-ops compute-completeness -ch`, advancing the
# source's watermark.
#
# Why per-source + chunked (not one global `-from min(watermark)` run):
#   1. The watermark write OVERWRITES (ON CONFLICT … SET watermark = EXCLUDED),
#      it does not max() — so a global run with `-to < tip` would REGRESS the
#      watermark of any source already further ahead. Iterating per-source with
#      each source's own watermark avoids that.
#   2. The high-volume SDEX projection reconcile blows ClickHouse's 12 GiB
#      per-query memory limit above ~30k ledgers (verified: 25k OK, 68k OOMs).
#      CHUNK=25000 keeps every window safe.
# Self-healing: any backlog — the initial 17-21-day catch-up or a post-outage
# gap — is walked automatically; in steady state each source is one ~daily-sized
# chunk, so a normal tick is fast.
#
# Same DSN sourcing as compute-archive-to.sh (peer-auth fails under systemd's
# restricted user-switch context).
set -euo pipefail
. /etc/default/stellarindex

DSN="$STELLARINDEX_POSTGRES_DSN"
CHUNK="${COMPLETENESS_CHUNK:-25000}"
CH_ADDR="${CH_ADDR:-127.0.0.1:9300}"
CONFIG="${CONFIG_PATH:-/etc/stellarindex.toml}"
OPS=/usr/local/bin/stellarindex-ops

TIP=$(psql "$DSN" -tA -c \
  "SELECT last_ledger FROM ingestion_cursors WHERE source='ledgerstream'" 2>/dev/null | tr -d '[:space:]')
if [ -z "$TIP" ] || [ "$TIP" = "0" ]; then
  echo "compute-completeness: ledgerstream tip unresolved; bailing" >&2
  exit 1
fi

# Latest watermark per source from the prior snapshots. If a source has no
# snapshot yet it simply won't appear here — an operator seeds the initial
# full verify (`compute-completeness -ch -source <s>`) once; thereafter this
# driver keeps it fresh.
SRCS=$(psql "$DSN" -tA -F' ' -c \
  "SELECT DISTINCT ON (source) source, watermark_ledger
     FROM completeness_snapshots ORDER BY source, computed_at DESC" 2>/dev/null)
if [ -z "$SRCS" ]; then
  echo "compute-completeness: no prior snapshots; operator must seed an initial verify" >&2
  exit 1
fi

rc=0
echo "compute-completeness: refresh to tip=$TIP (chunk=$CHUNK)"
while read -r SRC WM; do
  [ -n "$SRC" ] || continue
  FROM=$WM
  while [ "$FROM" -lt "$TIP" ]; do
    TO=$(( FROM + CHUNK )); [ "$TO" -gt "$TIP" ] && TO=$TIP
    if ! "$OPS" compute-completeness -config "$CONFIG" -ch -ch-addr "$CH_ADDR" \
         -source "$SRC" -from "$FROM" -to "$TO" </dev/null; then
      echo "compute-completeness: $SRC [$FROM,$TO] FAILED; skipping rest of this source" >&2
      rc=1
      break
    fi
    FROM=$TO
  done
done <<EOF
$SRCS
EOF
echo "compute-completeness: pass complete (tip=$TIP)"
exit $rc
