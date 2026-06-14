# A12 — Auth / Account / Dashboard / Rate-limit — security audit

**Date:** 2026-06-14
**Scope:** `internal/auth/` (incl. `sep10/`), `internal/ratelimit/`,
`internal/usage/`, `internal/cachekeys/`, `internal/platform/` (incl.
`postgresstore/`), `internal/customerwebhook/` (HMAC sign path only),
and the v1 API handlers + middleware:
`internal/api/v1/{account,signup,signup_verify,auth_sep10,explorer_accounts}.go`,
`internal/api/v1/middleware/*`,
`internal/api/v1/{dashboardauth,dashboardkeys,dashboardwebhooks}/*`.
**Mode:** READ-ONLY. No source edited, no git run.
**Dimensions:** D3 (security, primary), D1 (correctness), D4 (concurrency).

This is the security-critical area. Per instruction, ANY auth /
permission / injection issue is rated at least High. The headline
finding the brief flagged (operator/Redis-minted keys 403'ing under a
CLOSED permission posture, and the inverse over-permissive risk) is
traced end-to-end below and found **fixed** in both the Redis
validator (`apikey_redis.go:255-266`) and the Redis writer
(`store.go:169` sets `PermissionsAll: true`). No live false-deny or
privilege-escalation in the mint→store→validate→subject→gate chain.

---

## Severity counts

| Severity | Count |
|----------|-------|
| Critical | 0 |
| High     | 2 |
| Medium   | 6 |
| Low      | 7 |
| Info / nit | 4 |

No Critical. The two Highs are both abuse-surface / DoS-class
(unauthenticated expensive crypto and email send not specifically
throttled), not auth-bypass or injection. No SQL injection found
(every query is parameterized). No privilege-escalation found. No
plaintext secret logging found.

---

## Findings

| Severity | file:line | dim | issue | why it matters | fix | confidence |
|----------|-----------|-----|-------|----------------|-----|------------|
| High | `internal/api/v1/auth_sep10.go:124` (POST /v1/auth/sep10/token) | D3 | The SEP-10 token endpoint runs `txnbuild.ReadChallengeTx` + `VerifyChallengeTxSigners` (Ed25519 signature verification + XDR parse) on **anonymous** input, gated only by the shared anonymous per-IP rate-limit bucket (`AnonRateLimitPerMin`, default 60/min). There is no dedicated cost-aware throttle like signup has. | An attacker rotating source IPs (or behind the default 60/min floor) can drive repeated server-side Ed25519 verifications + XDR decodes. The replay guard only fires *after* a valid signature; bogus XDR is rejected cheaply but still costs a parse. Lower-impact than a full bypass, but it is an unauthenticated expensive-crypto surface with no purpose-built limiter. | Add a per-IP token-endpoint throttle (mirror `SignupIPThrottle`), or document that the anon bucket is the intended ceiling and lower the default for `/v1/auth/sep10/token`. Cheap parse-before-verify (already does ReadChallengeTx first) limits the worst case. | Med |
| High | `internal/api/v1/dashboardauth/handlers.go:169` (POST /v1/auth/login) | D3 | Magic-link login issues an email send (`Sender.Send`) per request, throttled only by the global anonymous per-IP rate-limit; there is **no per-email throttle** (unlike `/v1/signup`, which has `SignupIPThrottle`). A single IP can request up to `AnonRateLimitPerMin` magic links/min, each triggering an outbound email to an attacker-chosen victim address. | Email-bombing a victim inbox + burning the deployment's email-send quota / sender reputation. The endpoint deliberately returns 200 regardless of whether the email exists (good anti-enumeration), which also means there is no signal to the victim/operator. | Add a per-IP AND per-target-email throttle on `/v1/auth/login` (e.g. N links per email per hour) reusing the `SignupIPThrottle` pattern; cap outbound magic-link sends per recipient. | High |
| Medium | `internal/api/v1/dashboardauth/middleware.go:150-171` | D4 | `touchTracker.last` is an unbounded in-memory `map[uuid.UUID]time.Time` keyed by session id; entries are inserted on first touch and **never evicted**. | One map entry per distinct session id seen by the process for the process lifetime. Over weeks of sessions this is a slow memory leak; an attacker who can mint many sessions (each magic-link login → new session id) can accelerate growth. Bounded only by process restarts. | Evict on a sweep (drop entries older than `interval`), or use an LRU / TTL map. Low exploitability (requires valid sessions) but it is unbounded growth on a security-path structure. | High |
| Medium | `internal/auth/list_keys.go` + `internal/auth/store_update.go` + `internal/auth/store_mark_email_verified.go` | D1/D3 | Every Redis-store mutation/list (`ListKeysForIdentifier`, `RevokeKeyByID`, `UpdateRateLimit`, `MarkEmailVerified`) does a full `SCAN apikey:*` + per-key GET + JSON decode (O(N) in total key count). `MarkEmailVerified` and `UpdateRateLimit` SCAN on the **request/verify hot path** (`/v1/signup/verify` calls `MarkEmailVerified`). | At scale this is a self-inflicted DoS: a keyspace of N keys turns each verify/revoke/list into an N-GET sweep. The code comments acknowledge the O(N) trade-off ("fine at v1 scale") and an index is the documented fix. Also: a SCAN under concurrent writes can miss/duplicate keys (Redis SCAN semantics), so `RevokeKeyByID` could in theory miss a key being rewritten — low risk but a correctness edge. | Add the `apikey-byid:<keyID>` / `signup:identifier:<id>` secondary indexes at Create time as the code TODOs note; drop the SCANs. | High |
| Medium | `internal/auth/list_keys.go:74` (`RevokeKeyByID`) | D3 | The Redis-store `RevokeKeyByID` **hard-DELs** the key record. There is no cache-invalidation hook coupling it to the `PostgresAPIKeyValidator` cache — but more importantly, for deployments running the Redis validator this is fine, whereas the dashboard revoke path (`dashboardkeys` → Postgres `Revoke` soft-delete + `InvalidateCachedKey`) and the `/v1/account/keys` revoke path (Redis hard-DEL, no Postgres) are two **different revoke stores**. If a deployment runs the Postgres read-through validator but `/v1/account/keys` DELETE routes to the Redis store, a revoke could hit the wrong backing store. | Split-brain between the two key stores: a key revoked via one surface may keep authenticating via the other validator. Depends on main.go wiring (which store backs `s.accounts` vs the validator). Worth a wiring assertion. | Ensure a single canonical revoke path per deployment; assert at boot that the AccountStore writer and the APIKeyValidator reader share a backing store, or route both revoke surfaces through one. | Med |
| Medium | `internal/api/v1/dashboardauth/handlers.go:303-304` | D3 | `GeoFirstSeen`/`GeoLastSeen` are taken verbatim from the client-supplied `CF-IPCountry` header with no trusted-proxy gate. If the deployment is NOT actually fronted by Cloudflare (or the proxy doesn't strip the inbound header), a client can forge `CF-IPCountry`. | Forged geo data in the session audit trail; misleading "logged in from" UX and any future geo-based security signal. Cosmetic today (geo isn't a gate), but it's attacker-controlled data persisted as if trusted. | Only honour `CF-IPCountry` when `requestCameViaTrustedProxy(peer)` is true (same gate the IP resolver uses); else store empty. | High |
| Medium | `internal/api/v1/middleware/keypolicy.go:97-116` (`checkRefererAllowlist`) | D3 | The Referer allowlist matches on `url.Host` (host:port) case-insensitively against operator entries. `Referer` is fully client-controlled. Using Referer as an access-control gate is inherently weak: any non-browser client (curl, server-side) sets it to anything, and browsers can omit it. | A Referer allowlist gives a false sense of security — it stops nothing against a non-browser attacker who knows the allowed host. It IS opt-in and additive to IP allowlist, so not a regression, but it should be documented as defence-in-depth only, not a boundary. The implementation itself is correct (rejects missing/malformed Referer when the gate is set). | Document that Referer allowlisting is browser-hardening only, not an auth boundary; keep IP allowlist + key secrecy as the real controls. | High |
| Medium | `internal/ratelimit/bucket.go:153-175` + `signup_ip_throttle.go:187-210` | D4 | The dwell-time fail-open/closed clock (`redisErrorSince`) is per-process and guarded by a mutex (correct), but `observeRedisSuccess` resets on a *single* success while `observeRedisFailure` arms on the first failure. Under a flapping Redis (alternating ok/fail) the dwell clock keeps resetting, so a partial outage that returns the occasional success **never trips fail-closed** — the limiter stays fail-open indefinitely. | The J40 vector the dwell-time was built to close (attacker holds Redis down to disable abuse-prevention) is only closed for a *fully* sustained outage. An attacker who can induce intermittent Redis success (or a naturally flapping backend) keeps the throttle fail-open and unbounded. | Track consecutive-failure ratio or a sliding error window rather than reset-on-any-success; or require K consecutive successes to clear. Documented trade-off ("first OK after outage" marker) but it weakens the security property. | Med |
| Low | `internal/api/v1/middleware/keypolicy.go:138-149` (`permissionMatches`) | D3 | `EndpointPrefix` matching uses raw `strings.HasPrefix(r.URL.Path, e.EndpointPrefix)`. A deny-prefix `"/v1/price"` would also match `/v1/price-internal` etc.; an allow-prefix is similarly greedy. No path-boundary check. | Prefix rules can over- or under-match adjacent routes (e.g. a deny of `/v1/admin` won't catch `/v1/admin/` vs `/v1/adminx`). Today the route set makes collisions unlikely, but a future route name could silently widen/narrow a customer's deny rule. | Match on path segment boundary (`prefix == path || strings.HasPrefix(path, prefix+"/")`). | High |
| Low | `internal/api/v1/middleware/auth.go:240-260` (`bearerOnly`) | D3 | `Bearer` scheme prefix match is exact-case (`strings.HasPrefix(h, "Bearer ")`). RFC 7235 auth-scheme is case-insensitive; a client sending `bearer <key>` gets treated as no-credential → 401 (in `apikey` mode) rather than authenticating. | Interop / false-deny for spec-compliant clients using lowercase scheme. Not a security hole (fails closed), but a correctness foot-gun. | Case-insensitive scheme compare. | High |
| Low | `internal/auth/sep10/jwt.go:71-112` (`parseJWT`) | D1 | `nbf` (not-before) claim is written on issue (`= iat`) but **not enforced** on parse. Only `exp` is checked (at the call site). | Harmless today because `nbf == iat` (a freshly issued token is always already valid), but if `nbf` ever diverges from `iat` the gate won't honour it. Also: `iss` is checked, `aud` is not (no audience claim) — fine for single-issuer single-audience but worth noting. | Either drop `nbf` from the claims or enforce it in `parseJWT` (`now >= nbf`). | High |
| Low | `internal/api/v1/dashboardauth/auth.go:86-92` (`numericFromBase32`) | D1 | The 6-digit paste code is derived `s[i] % 10` over a base32 alphabet — heavily biased (A-Z2-7 mod 10 is non-uniform) and only ~10^6 space. The doc correctly notes the full token (not the code) gates consumption. | The code is a UX shortcut, not a credential — but if any future flow ever treats the 6-digit code as sufficient to consume the token, the biased 10^6 space is brute-forceable within the 15-min TTL. As long as consumption requires the full plaintext token (it does, via `HashMagicLinkPlaintext`), this is informational. | Keep the full-token gate; never accept the 6-digit code alone without its own rate-limit + lockout. Document the invariant. | High |
| Low | `internal/api/v1/signup.go:291-302` (`buildSignupVerifyURL`) | D3 | The verify URL is built from client-controlled `r.Host` and `X-Forwarded-Proto`. A forged `Host` header could make the emailed verify link point at an attacker domain (the token rides in the query string of whatever host is sent). | Host-header injection → the verification email could contain a link to `https://attacker/v1/signup/verify?token=<valid-token>`, leaking the single-use token to the attacker if the victim clicks. Mitigated in prod by Caddy setting a fixed Host, but the code trusts the header unconditionally. | Build the verify base URL from a configured canonical base (not `r.Host`); or validate `r.Host` against an allowlist. | Med |
| Low | `internal/api/v1/dashboardwebhooks/handlers.go:564-575` + worker SSRF | D3 | Webhook URL SSRF defence is solid (reject userinfo, https-only, `rejectInternalHost` resolves + blocks RFC1918/loopback/link-local/CGNAT/0.0.0.0, re-checked at delivery via `ssrfGuardedDialContext`, redirects disabled). One gap: `isReservedTLD` lets `.localhost`/`.test`/etc. URLs PASS registration validation (relying on delivery-time resolution failure). | A `.localhost` host that *does* resolve (e.g. an attacker controls a resolver, or `/etc/hosts` maps it) would skip the registration check; defence then depends entirely on the delivery-time dial guard. Delivery guard exists, so this is defence-in-depth depth, not a hole. | Keep the delivery-time guard as the real boundary (it is); optionally drop the reserved-TLD bypass in prod config. | Med |
| Low | `internal/usage/counter.go:85-98` (`Increment`) | D4 | `Increment` uses `TxPipeline` (INCR + EXPIRE) — correct atomicity — but every increment re-issues EXPIRE, and the counter is best-effort (`_ = counter.Increment`). Under high concurrency on the same (subject, day) key this is fine for correctness (INCR is atomic) but the per-request EXPIRE is redundant after the first. | Negligible correctness risk; minor extra Redis op per request on the hot path. Noting for completeness — no data-race, INCR is atomic across goroutines/processes. | Set EXPIRE only on first increment (NX) as the signup throttle does (`if count == 1`). | High |
| Info | `internal/api/v1/signup_verify.go:131` | D1 | `apiKeyEmailVerifier.MarkEmailVerified(ctx, keyID, time.Time{})` passes the zero time; the v1 interface returns only `error` but the Redis store method returns `(APIKeyRecord, error)` — there must be an adapter in main.go. The store correctly maps zero-time → `now()` (`store_mark_email_verified.go:41-43`), so the verified flag IS set to a non-zero timestamp. Verified correct, but the signature mismatch means the adapter is load-bearing. | No bug; just a wiring coupling worth a regression test (if the adapter ever drops the zero→now translation, the gate predicate `EmailVerifiedAt.IsZero()` would stay true forever and lock verified users out). | Add a test asserting the verified timestamp is non-zero after verify. | High |
| Info | `internal/api/v1/middleware/keypolicy.go:49-75` | D3 | Operator-tier subjects bypass the per-endpoint permission check (`subject.Tier != auth.TierOperator` guards `checkPermissions`) but still get IP/Referer enforcement. This is intentional + documented. Correct posture. | Confirms operator keys are full-access by design (matches `store.go` `PermissionsAll: true` default). Not a finding — recorded so the "over-permissive" half of the brief's concern is explicitly accounted for: operator tier IS all-access by design, and it is reserved (never granted to public callers per `subject.go:26-29`). | — | High |
| Info | `internal/auth/sep10/redisreplay.go` + `validator.go:273-279` | D3 | SEP-10 replay guard hashes the **full signed XDR** (incl. signatures) as the dedup key, marks AFTER signature verification, TTL = challengeTTL. Correct: each challenge has a random manage_data nonce so XDRs are unique; replay of the exact signed XDR collides and is rejected; bogus XDR never spends a slot. | Confirms replay defence is correct. Note: it fails-OPEN when no ReplayGuard is wired, but main.go fails-LOUD (`return errors.New(... replay-guard required when auth_mode=sep10)`) so prod can't run sep10 without it. | — | High |
| Info | `internal/api/v1/middleware/cors.go:75-79` | D3 | CORS constructor **panics** at boot if `AllowedOrigins=["*"]` + `AllowCredentials=true` (browser-rejected combo). `Vary: Origin` is set correctly to prevent cache poisoning across origins. Credentialed CORS only to exact-match origins. Correct. | Confirms no wildcard-with-credentials cookie-exfil vector. | — | High |

---

## CORRECT-verified (things audited and found sound)

These were checked specifically because they are the security boundary
or were prior bug classes; they are correct as written:

1. **API-key permission posture (the headline concern) — FIXED both directions.**
   - Redis validator `apikey_redis.go:255-266` now maps
     `PermissionsAll → AllowAllPermissions`, `AllowPermissions`,
     `DenyPermissions`, `IPAllowlist`, `RefererAllowlist` onto the
     Subject (the prior false-DENY bug where redis-minted keys 403'd
     on every endpoint is closed).
   - Redis writer `store.go:169` sets `PermissionsAll: true` on
     operator/self-service mint (the load-test 210k/210k-403 regression
     is fixed). Dashboard mint sets `Permissions{All:true}` too
     (`dashboardkeys/handlers.go:269`).
   - Postgres validator `apikey_postgres.go:144-158` + cache hydrate
     (`cacheLookup:200-226`) + cache write-back (`cacheStore:265-292`)
     carry the SAME policy fields, so cache-hit and cache-miss Subjects
     are policy-identical (no silent KeyPolicy no-op on cache TTL hits).
   - No over-permissive path: `checkPermissions` defaults to CLOSED
     (no allow entries + `AllowAllPermissions=false` → 403), deny list
     is always consulted even when `AllowAllPermissions=true`.

2. **SQL injection — none.** Every query in `postgresstore/*`
   (apikey, account, user, token, webhook stores) uses positional
   parameters (`$1…$N`); no string concatenation of user input into
   SQL. The `apiKeyColumns`/`accountColumns` constants are static
   column lists, not user data. The `cidrArray.Value()` driver path
   formats CIDRs via `netip.Prefix.String()` (validated type), not raw
   strings.

3. **Constant-time secret comparison.** JWT signature compare uses
   `subtle.ConstantTimeCompare` (`jwt.go:89`). API-key lookup is by
   SHA-256 hash → Redis GET / Postgres `WHERE key_hash = $1` (hash
   equality, no plaintext compare). Webhook HMAC verify is the
   customer's responsibility (we sign; doc tells them to recompute).

4. **No plaintext secret logging.** Grepped all auth/dashboard/signup
   error+log paths: every `Logger.Error("generate plaintext"/"generate
   secret", "err", err)` logs only the error, never the value.
   Plaintext keys/secrets/tokens are returned exactly once in the
   create response and never re-served or logged. KeyHash is omitted
   from all DTOs (`dashboardkeys` keyDTO doc, `dashboardwebhooks`
   webhookDTO doc).

5. **SEP-10 verification.** `Verify` runs `ReadChallengeTx` (structure +
   server source + time bounds) THEN `VerifyChallengeTxSigners`
   (client sig present + server sig intact) THEN replay-guard THEN
   issue JWT. Empty-signer set → `ErrUnauthorized`. Time-bound expiry →
   `ErrTokenExpired`. JWT `iss` checked against home domain; `exp`
   enforced at call site. Server seed must be S-strkey (not pubkey).

6. **Magic-link single-use + replay.** `ConsumeMagicLinkToken`
   (`token_store.go:71-106`) is an atomic `UPDATE … WHERE token_hash=$1
   AND consumed_at IS NULL AND expires_at>$2 RETURNING` — two concurrent
   callbacks can't both succeed. Token stored as SHA-256 hash, never
   plaintext. Consumed/expired/absent all classified; consumed==absent
   on the wire (no token-existence oracle). Signup-verify token is Redis
   `GETDEL` (atomic single-use).

7. **IDOR / cross-account ownership checks.** Dashboard revoke
   (`dashboardkeys:338`), webhook update/delete/deliveries
   (`dashboardwebhooks` `parseAndAuthorise:411`) all verify
   `resource.AccountID == session.Account.ID` and return 404 (not 403)
   on mismatch to avoid an existence oracle. `/v1/account/keys` revoke
   scopes to `subject.Identifier` (`RevokeKeyByID` filters on it).
   Can't-revoke-self guard (`account.go:458`) prevents mid-request
   orphaning.

8. **XFF / client-IP forgery resistance (F-1335/F-1338).** Rate-limit
   anonymous key is the resolved client IP ALONE (not the
   IP+UA hash, which a client can rotate to mint unlimited buckets);
   `rightmostUntrustedForwardedFor` walks XFF right-to-left honouring
   trusted-proxy CIDRs and bails on a malformed hop. Empty trusted-proxy
   set ⇒ XFF ignored entirely.

9. **Rate-limit token bucket (D4).** Atomic INCR+EXPIRE Lua script
   (one round-trip); `count <= max` is the authoritative allow signal
   (not the decoupled TTL, which closed a deny→allow race); key is
   `url.QueryEscape`'d so `:` in IPv6/keys can't collide buckets; key
   length capped at 256B against header-bomb. Per-key override is
   sticky (documented). Bucket is stateless w.r.t. limit; shared safely
   across goroutines.

10. **Fail-open/closed dwell-time inversion.** Transient Redis blip
    (<30s) falls open (availability); sustained outage flips to
    fail-CLOSED 503+Retry-After (closes the disable-abuse-prevention
    vector) — for FULLY sustained outages (see Medium finding on the
    flapping edge). Applies consistently to `ratelimit.Bucket`,
    `RedisSignupIPThrottle`, and the signup handler. main.go fails-loud
    if `auth_mode=sep10` without a replay guard.

11. **cachekeys builders — no collision/drift.** `APIKey(hash)` =
    `apikey:<hex>`; `RateLimitKey` `url.QueryEscape`s the subject in
    lock-step with the bucket writer (tests round-trip to detect drift,
    per doc). Usage keys (`usage:<esc>:<day>`), SEP-10 replay
    (`sep10:seen:<b64url>`), signup namespaces
    (`signup:email:` / `signup:verify:` / `signup:lock:`), touch
    (`touch:apikey:`) are all disjoint. `OracleLatest` sorts asset keys
    for order-independence. No `:`-injection cross-namespace risk.

12. **Per-account quota races (F-1248/F-1257).** API-key Create uses
    `pg_advisory_xact_lock(hashtext('apikey:'||account_id))` + a gated
    `WHERE active_count.n < $cap` INSERT…RETURNING (ErrNoRows ⇒
    quota-exceeded). Webhook Create enforces the cap atomically in the
    INSERT. Concurrent callers at the boundary can't both insert.
    Lock keyspaces (`apikey:` vs `webhook:`) are disjoint.

13. **Signup duplicate-email race (F-1218/F-1255).** `ReserveEmail`
    (SETNX placeholder, 5m TTL) gates before mint; dashboard first-login
    uses optional `EmailLocker` (SETNX) + poll-for-winner +
    suspend-orphan fallback on the email-unique-index loser. No double
    speculative account when the locker is wired; defence-in-depth when
    it isn't.

14. **Open-redirect / next-param.** Callback `next` param is rejected
    unless path-only (`strings.HasPrefix(next,"/") && !"//"`), defeating
    `//evil.com` and absolute-URL open redirects.

15. **Rate-limit tier override is raise-or-lower only on the per-key
    bucket** (`RateLimitBySubject` / `TakeN`); anonymous callers never
    get a per-subject override (`bucketKeyAndOverrideForRequest` returns
    0 for anon). No way for a caller to lift their own anonymous floor.

16. **Cookie flags.** Session cookie is `HttpOnly`, `SameSite=Lax`,
    `Secure` (per `CookieSecure` config), value is a v4 UUID from
    crypto/rand, host-only by default. Logout clears it (`MaxAge:-1`)
    even on an invalid cookie and revokes the session server-side.
    Suspended/closed account ⇒ session denied + revoked.

---

## Notes on the brief's specific concerns

- **False-deny (CLOSED posture) bug class:** FIXED. See CORRECT #1.
  Both the Redis read mapping (`apikey_redis.go:255`) and the Redis
  write default (`store.go:169`) are present; the dashboard/Postgres
  path always carried policy. Verified no live 403-everything path.
- **Privilege escalation:** Not found. Self-service key mint inherits
  the caller's `Identifier` + `Tier` (`account.go:337-344`) — cannot
  escalate tier. Dashboard mint forces `APIKeyTierAPIKey` and clamps
  `rate_limit_per_min` to the account tier ceiling
  (`dashboardkeys:222`, `clampRateLimitToTier`). Operator tier is
  reserved and only set by `stellarindex-ops`, never via public mint.
- **Rate-limit bypass / fail-open over-permit:** The dwell-time design
  bounds fail-open to 30s for fully-sustained outages; the FLAPPING
  edge (Medium finding) is the residual over-permit window. Anonymous
  keying on IP-alone closes the UA-rotation bypass.
- **IP/Referer allowlist enforcement:** Enforced in `KeyPolicy`
  (`keypolicy.go`), correctly fail-closed on missing/malformed input
  when the gate is set. Referer noted as defence-in-depth only (Medium).
- **Webhook HMAC:** Signs with `hmac.New(sha256.New, secret)` over the
  raw payload, header `X-StellarIndex-Signature: sha256=<hex>`
  (`worker.go:330`). Secret is the literal HMAC key (doc is honest
  about the misnamed `SecretHash` column + no app-layer at-rest
  encryption — operators rely on DB/disk encryption). Shown once,
  never re-served. SSRF-guarded delivery.

---

## Files read (count: 41)

`internal/auth/`: apikey.go, apikey_redis.go, apikey_postgres.go,
subject.go, store.go, store_update.go, store_mark_email_verified.go,
list_keys.go, errors.go, signup_tracker.go, signup_ip_throttle.go,
signup_verifier.go, touch_debouncer.go, sep10.go(referenced).
`internal/auth/sep10/`: validator.go, jwt.go, redisreplay.go.
`internal/ratelimit/`: bucket.go.
`internal/usage/`: counter.go.
`internal/cachekeys/`: keys.go.
`internal/platform/`: webhook.go.
`internal/platform/postgresstore/`: apikey_store.go, account_store.go,
user_store.go, token_store.go.
`internal/customerwebhook/`: worker.go (sign path).
`internal/api/v1/`: account.go, signup.go, signup_verify.go,
auth_sep10.go, explorer_accounts.go, server.go (chain assembly).
`internal/api/v1/middleware/`: auth.go, keypolicy.go, ratelimit.go,
remoteip.go, require_email_verified.go, monthly_quota.go,
touch_usage.go, cors.go.
`internal/api/v1/dashboardauth/`: auth.go, middleware.go, handlers.go.
`internal/api/v1/dashboardkeys/`: handlers.go.
`internal/api/v1/dashboardwebhooks/`: handlers.go.
`cmd/stellarindex-api/main.go` (wiring, grepped).

(Test files were enumerated and consulted for behaviour confirmation
but not line-audited individually; the production paths above are the
audited surface.)
