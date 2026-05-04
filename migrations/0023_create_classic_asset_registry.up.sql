-- 0023 up — `classic_assets` + `issuers` + `anchors`.
--
-- The classic-asset registry. Today every classic-asset trade is in
-- the `trades` hypertable but there's no first-class catalogue of
-- which `(code, issuer)` pairs exist, who issued them, or what
-- anchor stands behind the issuer. This migration creates the three
-- tables that flip "show every asset trading on SDEX" from "scan
-- the trades hypertable" to "browse a catalogue."
--
-- Three tables, layered:
--
--   `classic_assets` — one row per (code, issuer) ever observed.
--   Auto-populated by an observer (Phase 4) that hooks every trade
--   + every ChangeTrust op + every payment-crossing-an-issuer op
--   and upserts a row. Cardinality: ~5-10k assets on Stellar today.
--
--   `issuers` — one row per G-account that ever issued an asset.
--   Captures auth flags (auth_required, auth_revocable,
--   auth_immutable, auth_clawback) which the issuer-account-state
--   tracker keeps current via the `accounts` decoder. Also holds
--   the SEP-1 stellar.toml payload as jsonb so per-issuer reads
--   don't need to refetch.
--
--   `anchors` — one row per home_domain. An anchor (e.g. Centre,
--   Anchorage, Stably) often runs multiple G-accounts; this table
--   aggregates the SEP-1 metadata once per domain rather than
--   duplicating it per issuer.
--
-- The SEP-1 fetcher (Phase 4) is the only outbound HTTPS we add to
-- the system. Per-domain rate-limited, retried with exponential
-- backoff, weekly stale-cache acceptance.
--
-- Powers `/coins/{slug}` (classic asset detail), `/issuers/{G}`,
-- `/anchors/{home_domain}` per docs/architecture/showcase-site-data-inventory.md
-- §7.13 / §7.14.

BEGIN;

CREATE TABLE classic_assets (
    -- Canonical asset identifier — "{code}-{issuer}".
    asset_id          text         PRIMARY KEY,

    -- Decomposed for indexing.
    code              text         NOT NULL,
    issuer_g_strkey   text         NOT NULL,

    -- URL-safe slug. Auto-generated lowercase code; disambiguated
    -- with a short issuer prefix when multiple issuers issue assets
    -- with the same code (e.g. usdc-circle vs usdc-anchor). UNIQUE
    -- so `/coins/{slug}` resolution is deterministic.
    slug              text         UNIQUE,

    -- First/last time we saw activity for this asset. last_seen_*
    -- updates on every trade or ChangeTrust referencing the asset.
    first_seen_at     timestamptz  NOT NULL,
    first_seen_ledger integer      NOT NULL CHECK (first_seen_ledger >= 0),
    last_seen_at      timestamptz  NOT NULL,
    last_seen_ledger  integer      NOT NULL CHECK (last_seen_ledger >= first_seen_ledger),

    -- Rolling counter — ticks on every observation. Cheap activity
    -- proxy for ranking + filtering ("active" = observation_count
    -- > N in last 30d).
    observation_count bigint       NOT NULL DEFAULT 0 CHECK (observation_count >= 0)
);

COMMENT ON TABLE classic_assets IS
    'Auto-populated registry of every classic asset observed on '
    'SDEX or in any other op. One row per (code, issuer); slug is '
    'the URL-safe identifier used by /coins/{slug}.';

-- Per-issuer lookup: "give me every asset issued by G-...".
CREATE INDEX classic_assets_issuer_idx ON classic_assets (issuer_g_strkey);

-- Code-only lookup: "is there a USDC?" — disambiguates by surfacing
-- every issuer that uses the code.
CREATE INDEX classic_assets_code_idx ON classic_assets (code);

CREATE TABLE issuers (
    -- The G-account that issued the asset.
    g_strkey            text         PRIMARY KEY,

    -- home_domain field on the account. Set by the issuer; we
    -- re-read it on every account-state change. NULL when the
    -- account has no home_domain set (legitimate; treated as
    -- unverified anchor).
    home_domain         text,

    -- Auth flags from the AccountFlags. Source of truth for the
    -- "Issuer Card" panel on /coins/{slug} and /issuers/{G}.
    auth_required       boolean,
    auth_revocable      boolean,
    auth_immutable      boolean,
    auth_clawback       boolean,

    -- SEP-1 stellar.toml payload + when we last successfully
    -- fetched it. Loose jsonb because SEP-1 schemas drift over
    -- time + many issuers add custom fields.
    sep1_resolved_at    timestamptz,
    sep1_payload        jsonb,

    -- When this G-account was created on-chain. Useful for the
    -- "age" indicator on the issuer card.
    creation_ledger     integer      CHECK (creation_ledger IS NULL OR creation_ledger >= 0)
);

COMMENT ON TABLE issuers IS
    'Per-G-account issuer state — auth flags, home_domain, cached '
    'SEP-1 payload. Auto-updated by the accounts decoder + SEP-1 '
    'fetcher worker.';

CREATE INDEX issuers_home_domain_idx ON issuers (home_domain);

CREATE TABLE anchors (
    -- The home_domain — primary identity. Many G-accounts can
    -- share one home_domain, so this is the natural aggregation
    -- key.
    home_domain          text         PRIMARY KEY,

    -- SEP-1 payload + parsed convenience fields. Parsed fields
    -- are denormalized from the full payload for fast reads.
    org_name             text,
    description          text,
    contact_email        text,

    sep1_payload         jsonb,
    sep1_resolved_at     timestamptz,

    -- Resolution status — distinguishes "we tried and got a
    -- valid stellar.toml" from "we tried and the fetch errored"
    -- so the UI can show appropriate badges.
    sep1_resolved_status text         NOT NULL DEFAULT 'pending'
                                      CHECK (sep1_resolved_status IN
                                            ('pending','ok','fetch_failed','parse_failed','tls_failed'))
);

COMMENT ON TABLE anchors IS
    'Aggregated SEP-1 metadata per home_domain. One row per domain '
    'regardless of how many G-accounts the anchor runs.';

COMMIT;
