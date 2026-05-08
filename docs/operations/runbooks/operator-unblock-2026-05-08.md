# Operator unblock — 2026-05-08

**Context.** Three queued ships need operator action. Each is independent.

---

## 1. Bump GitHub Actions spending cap

**Symptom.** Every `gh workflow run deploy.yml` (or `explorer-deploy.yml` /
`docs-deploy.yml`) fails immediately with the annotation:

> The job was not started because an Actions budget is preventing further use.

**Why this matters now.**

- `v0.5.0-rc.34` is tagged + binaries published, but the r1 deploy is
  blocked. r1 is running `v0.5.0-rc.33` (the API; the indexer is still
  on `rc.29`).
- Several frontend ships are queued but the manual `workflow_dispatch`
  trigger on `explorer-deploy.yml` is also Actions-runner-backed and
  hits the same cap.

**Fix.** GitHub → Org Settings → Billing & plans → Actions usage →
raise the spending limit (or wait for the next billing cycle).

**Verify.** `gh workflow run deploy.yml -f region=r1 -f version=v0.5.0-rc.34
-f binaries=ratesengine-api` should now queue + succeed.

---

## 2. Deploy `v0.5.0-rc.34` to r1 — all three binaries

Once Actions runs again, deploy:

```sh
gh workflow run deploy.yml \
  -f region=r1 \
  -f version=v0.5.0-rc.34 \
  -f binaries=ratesengine-api,ratesengine-aggregator,ratesengine-indexer
```

The `ratesengine-indexer` slot is the key one. r1's indexer has been
on `rc.29` since 2026-05-07 — it predates the SAC-wrapper-aware
`usd_volume` insertion path. Every Soroban DEX trade ingested since
then has been written with `usd_volume = NULL`, which is why Comet
shows `$0` 24h volume on `/sources` despite 37 trades. Aquarius and
Phoenix trade volumes are similarly under-counted.

**Verify post-deploy:**

```sh
curl -sS https://api.ratesengine.net/v1/version | jq '.data.version'
# expect: "v0.5.0-rc.34"

curl -sS 'https://api.ratesengine.net/v1/sources?include=stats' \
  | jq '.data[] | select(.name=="comet")'
# new trades will start showing volume_24h_usd > 0 within minutes
```

---

## 3. Backfill historical Soroban DEX `usd_volume`

After the indexer deploy, **historical NULL rows** still need
correction. ~124K Soroban DEX trades (Aquarius 104K, Phoenix 8K,
Soroswap 8K, Comet 38) were ingested before the SAC config landed
and remain NULL.

Script: `scripts/ops/recompute-usd-volume-soroban.sql` is idempotent
(filters `WHERE usd_volume IS NULL` so re-runs are safe).

```sh
ssh root@r1
PGPASSWORD=$(cat /etc/ratesengine/postgres-password.txt) \
  psql -h 127.0.0.1 -U ratesengine -d ratesengine \
       -v ON_ERROR_STOP=1 \
       -f /path/to/recompute-usd-volume-soroban.sql
```

Expect ~124K row updates. Per-source post-fix volume on the
`/v1/sources` envelope should land:

- Aquarius: ~$10–15M (24h) — order of magnitude
- Comet: ~$11K (24h) — matches BLND/USDC backstop activity
- Soroswap, Phoenix: similar correction
- Hourly continuous-aggregates rebuild on the next refresh tick.

---

## 4. Populate `[supply]` watched-sets on r1 (longer-running)

`/v1/coins` returns `circulating_supply: null` and `market_cap_usd:
null` for every asset because none of the three supply algorithms
are running on r1.

**Required config.** Add to `/etc/ratesengine.toml`:

```toml
[supply]
sdf_reserve_accounts = [
  # G-strkey list — pull current set from SDF's most recent
  # reserve-move announcement (forum post; periodic).
]
aggregator_refresh_enabled = true
watched_classic_assets = [
  "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
  "EURC-GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2",
  "AQUA-GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA",
  "yXLM-GARDNV3Q7YGT4AKSDF25LT32YSCCW4EV22Y2TV3I2PU2MMXJTEDL5T55",
  "SHX-GDSTRSHXHGJ7ZIVRBXEYE5Q74XUVCUSEKEBR7UCHEUUEK72N7I7KJ6JH",
]

[supply.reserve_balances_stroops]
# bootstrap fallback — one entry per sdf_reserve_accounts G-strkey.
# Operator-managed; updated when SDF announces a reserve move.
```

**Install the supply-snapshot timer:**

```sh
sudo cp deploy/systemd/supply-snapshot.{service,timer} /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now supply-snapshot.timer
sudo systemctl restart ratesengine-aggregator
```

**Verify.**

```sh
sudo systemctl start supply-snapshot.service
journalctl -u supply-snapshot.service --since '1 min ago' --no-pager
# expect: writer attributes a row to asset_supply_history for each
# watched asset class.

curl -sS https://api.ratesengine.net/v1/assets/native | jq '.data.circulating_supply'
# expect: a non-null integer-string after the next aggregator
# goroutine tick (~5 min default cadence).
```

---

## 5. After all four steps: confirm user-visible state

Hit each of these and confirm green:

| URL | Expectation |
|---|---|
| `/v1/version` | `version: v0.5.0-rc.34` |
| `/v1/coins?limit=5` | `circulating_supply` and `market_cap_usd` populated for at least XLM + USDC |
| `/v1/sources?include=stats` | `comet` volume_24h_usd > 0 |
| `/v1/issuers` | 27 anchors with org_name + 19 issuers with `scam_reason` |
| `https://ratesengine.net/aggregators/` | each card lists mainnet contract addresses |
| `https://ratesengine.net/divergences/` | Chainlink card lists 3 wired feeds |

When each row is green, the Phase-2 user-visible regression list from
2026-05-08 is closed.
