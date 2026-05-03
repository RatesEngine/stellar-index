---
title: Validator Rollout — 1 → 3 Full Validators as one Tier-1 Organisation
last_verified: 2026-05-02
status: draft — ratified at Week 2 design review
---

# Validator Rollout — 1 → 3, as one Tier-1 Org

**Owner:** @ash.
**Extends:** [ADR-0004 Tier-1 validator aspiration](../../adr/0004-tier1-validator-aspiration.md).
**Relates to:** [archival-node-spec.md](archival-node-spec.md),
[multi-region-topology.md](multi-region-topology.md).

This doc locks the phased rollout: **one Rates Engine organisation,
three full validators, geographically separated, brought up one at a
time.** We launch with one node in syncing state so we can shake out
bugs without multi-node coordination; the architecture is designed
so dropping in node 2 and node 3 adds no new shape — just more of
the same.

---

## 1. What "Tier-1 Organisation" actually means

Per SDF's `stellar-docs/docs/validators/tier-1-orgs.mdx` (read in
Phase 1; cited in
[discovery/data-sources/archival-nodes.md](../../discovery/data-sources/archival-nodes.md)):

A Tier-1 Organisation is a **single organisation** running:

- **≥ 3 full validators** — not archival watchers; voting members of
  SCP.
- **Geographically separated** — different regions / different
  upstream networks.
- **Independent history archives** — each validator publishes to
  storage it independently controls.
- **Org-level identity** — the three validators vote as one org in
  network trust decisions (via the SDF-maintained T1 Orgs list).

So: **Rates Engine = 1 org. We operate 3 validators. They're our
three validators, not three peer orgs' validators.** This matters
for:

- Quorum sets: our three nodes aren't independent trust anchors;
  they're the same org. SDF treats them as one org-vote.
- Key ceremony: all three validator keys trace to the same HSM
  backup ceremony and operational procedures.
- Compliance: the T1 Orgs listing names `Rates Engine` once, not
  three times.

---

## 2. Why we launch with 1 validator (not 3 on day one)

The conservative path:

1. **Shake out hardware + software config with a single node.**
   Three nodes hitting the same rare bug triples the firefighting
   cost.
2. **Measure real catchup times** on one node before committing to
   three colo contracts. Numbers in
   [archival-node-spec.md](archival-node-spec.md) §3.3.3 are
   extrapolations until we measure.
3. **Validate the archive + Galexie + stellar-rpc co-resident
   pattern** under real pubnet load. Adversarial audit §6d flagged
   this specifically.
4. **Establish the Vault / HSM / backup pipelines on one host**
   before we multiply them.

Launching with three simultaneously is achievable but high-risk
inside the 10-week window. The single-node phase is not optional —
it's a quality gate.

---

## 3. Rollout phases

### Phase A — Week 2–3: One archival node, **non-voting**

- Hardware: 1× node per [archival-node-spec.md](archival-node-spec.md).
- Role: archival full node. `NODE_IS_VALIDATOR=false`.
- Region: R1 (Hetzner FSN1, Falkenstein, DE — see
  [r1-deployment-state.md](../../operations/r1-deployment-state.md)).
- Quorum set: mirrors SDF's recommended quorum for a non-validating
  node (SDF × 3, LOBSTR, Satoshipay, Franklin Templeton) — we
  **depend on** the existing network, we don't yet contribute to it.
- Running: stellar-core in `CATCHUP_RECENT` (live), then promote to
  `CATCHUP_COMPLETE` once full archive mirrored.
- Publishing: starts publishing our history archive to our own
  MinIO (`history-archive/`) immediately. Anyone can read our
  archive; SDF doesn't have to ratify anything yet.

**Exit criteria (Phase A → Phase B):**

- [ ] Node has been live and synced for ≥ 7 consecutive days.
- [ ] Galexie + stellar-rpc + ratesengine-indexer all ingest from
      this node with zero gaps.
- [ ] Archive cross-checks (against SDF + 2 other T1 orgs' public
      archives) show hash parity.
- [ ] Measured memory footprint, catchup duration, NVMe throughput
      against the spec's estimates. Adjust procurement for nodes
      2–3 based on real numbers.

### Phase B — Week 4–5: Promote to validator, still 1 node

- Key ceremony: generate `NODE_SEED` on YubiHSM-2, witnessed by
  @ash + @alex, shamir-split backup to two safes.
- Config: `NODE_IS_VALIDATOR=true`, `NODE_SEED` resolved via the
  HSM signer daemon (never on disk).
- Register with SDF: submit our validator public key to the
  `stellar.toml` of `ratesengine.net` + the
  `stellar-docs/validators/tier-1-orgs.mdx` addition PR (when we
  have 3 validators). For now we're a standalone validator.
- Announce on `#validators` Discord so other operators can weight
  us in their quorum sets.

**Note:** a single validator is **not** a T1 org. It's a validator.
T1 status requires 3 validators. Phase B gives us the operational
muscle memory.

**Exit criteria (Phase B → Phase C):**

- [ ] Voted correctly on 100% of ledgers for 14 consecutive days.
- [ ] No incidents involving the validator key or HSM.
- [ ] Archive cross-check stayed green for 14 days.
- [ ] Runbook rehearsals complete for: HSM failure, validator-key
      rotation, core upgrade.

### Phase C — post-launch: Deploy validator 2 in R2 (AWS us-east-1, Ashburn)

- Hardware: 1× identical node, shipped to R2 colo.
- Key ceremony: second validator key, fresh on a second YubiHSM.
  Different key material than validator 1 — never the same key.
- Config: `NODE_IS_VALIDATOR=true`, distinct `NODE_SEED`,
  `NODE_HOME_DOMAIN=ratesengine.net` (same as validator 1; signals
  "same org").
- Quorum set of validator 2: same shape as validator 1's, except
  it adds validator 1 to its "ratesengine org" sub-quorum.
- Our quorum sub-quorum now has 2 members; SCP expects us to
  weight it as an org.
- Application-layer: region R2 joins the Patroni cluster as sync
  replica (per
  [multi-region-topology.md §5](multi-region-topology.md#5-application-state--timescaledb-the-single-writer-layer)).

**Exit criteria (Phase C → Phase D):** same as Phase B, applied to
validator 2.

### Phase D — post-launch: Deploy validator 3 in R3 (Vultr Singapore)

- Same pattern as Phase C.
- Application-layer: R3 joins Patroni as async replica. etcd grows
  from 3 to 5 nodes (spanning the 3 regions).
- Our org now has three validators in three regions with
  independent archives.

### Phase E — Week 9: Apply for SDF Tier-1 listing

- Precondition: all three validators voted correctly, their
  archives matched, and we published for ≥ 14 days.
- Action: open a PR to `stellar/stellar-docs` adding
  "Rates Engine" to the Tier-1 Orgs table, citing our 3 validators'
  public keys + public archive URLs.
- SDF reviews. Typical turnaround days to weeks.

### Phase F — post-launch: steady state

- 3 validators, 3 regions, 1 org, T1-listed.
- Ongoing work: protocol upgrades on the "3-of-4" rhythm, yearly
  validator-key rotations, quarterly HSM backup audits.

---

## 4. Quorum set shape per phase

**Phase A (archival non-voting):** we don't have a quorum set in the
"our vote" sense — we just follow the network. Our quorum is the set
we *trust* for our own ledger close decisions, same shape as any
other non-validating node:

```
THRESHOLD_PERCENT = 67
[QUORUM_SET]
  # SDF
  [[validators]] publickey = "GCG..." home_domain = "stellar.org"
  [[validators]] publickey = "GDC..." home_domain = "stellar.org"
  [[validators]] publickey = "GBC..." home_domain = "stellar.org"
  # T1 orgs
  [[validators]] publickey = "GAZ..." home_domain = "lobstr.co"
  [[validators]] publickey = "GBF..." home_domain = "lobstrco.com"
  [[validators]] publickey = "GD6..." home_domain = "satoshipay.io"
  # ... etc
```

**Phase B (one Rates Engine validator):** same quorum set we follow,
with us now voting. SDF + T1s + us = still one-vote-each.

**Phase C (two Rates Engine validators):** we're now an `org`. Each
of our two validators includes a sub-quorum for the `ratesengine.net`
home domain:

```
[[quorumSet]] threshold_percent=67
  [[validators]] home_domain="stellar.org" ...             # 3 SDF
  [[validators]] home_domain="lobstr.co" ...
  [[validators]] home_domain="ratesengine.net"
    [[validators]] publickey="G..." # validator 1
    [[validators]] publickey="G..." # validator 2
  ... other T1 orgs
```

At 2 validators, our org's sub-quorum has effective strength
slightly less than a 3-validator org — some downstream voters will
weight it less. This is why we don't promote to T1 listing until
we have 3.

**Phase D+ (three Rates Engine validators):** the `ratesengine.net`
sub-quorum contains all three; T1-compliant.

The exact TOML shapes land as PRs against `configs/validators/`
when each phase ships.

---

## 5. Key ceremony — specific procedures

Apply at Phase B, C, D (once per validator).

### 5.1 Materials

- Fresh YubiHSM-2, factory-reset, in an antistatic bag.
- Dedicated "ceremony" laptop, offline, freshly imaged from a known-
  good ISO.
- Two operators (@ash + @alex minimum).
- Witness camera (self-recorded, stored with the backup).
- Pre-printed Shamir-backup forms.

### 5.2 Steps

1. Power up ceremony laptop offline. Airgapped.
2. Generate the keypair on the HSM via
   `yubihsm-shell generate asymmetric ed25519`. Public key exported;
   private key **never** leaves the HSM.
3. Record the public key + fingerprint; two operators sign off.
4. Generate Shamir split of the HSM backup (3 shares, threshold 2);
   print on sealed forms.
5. Store each share in a separate safe (different physical
   locations — at least one bank-deposit-box-equivalent).
6. Wipe the ceremony laptop.
7. Install the HSM in the validator host's USB slot.
8. `stellar-core` config: `NODE_SEED` → HSM signer daemon at
   `unix:///var/run/ratesengine-signer.sock`.
9. Sign-off: both operators attest the ceremony completed correctly.
10. File the public-key record + ceremony log in
    `configs/validators/<name>/ceremony.txt` (public-key + metadata
    only; no secret material).

### 5.3 Recovery

If an HSM fails: reconstruct from any 2 of the 3 Shamir shares
onto a replacement HSM. Operators responsible for the safes
rendezvous, reconstruct, re-install. RTO goal: < 48 h.

If a validator key is **suspected** compromised: emergency rotation.

- Broadcast on `#validators` Discord that the key is being rotated.
- Generate new keypair (new ceremony).
- Update our `stellar.toml` + any quorum-set references.
- Peers update their config.

---

## 6. What has to be right on day one to make the rollout trivial

Design-time work that pays back at Phase C/D:

- **Every config is Ansible-templated, not hand-edited.** Node
  identity (home domain, HSM socket path, quorum-set TOML) is the
  only per-node diff. `ansible-playbook deploy-validator.yml --limit r2`
  should stand up validator 2 identically to validator 1 without a
  human retyping config.
- **Observability is region-aware from day one.** Prometheus labels
  include `region=r1|r2|r3`. A dashboard that works for 1 node
  still works for 3 without renaming panels.
- **Archive cross-check runs even with 1 validator.** It compares
  our archive against SDF + LOBSTR + Satoshipay. At 3 validators
  it additionally compares our three archives against each other.
  Same script, different config.
- **Application layer ships multi-region-ready.** Patroni +
  Timescale + Redis all follow the multi-region-topology.md design
  even when there's only 1 region. Standing up R2 is "join the
  cluster", not "redesign the cluster."
- **Runbooks are region-agnostic.** Runbook says "the affected
  region's Patroni node"; works whether there are 1 or 3.

---

## 7. Scope boundary — what validator status does NOT change

Our pricing service does not depend on validator status for any
correctness property. A validator adds:

- A vote in SCP (contribution to the network).
- Publication of a history archive (public good).
- Eligibility for T1 listing (trust signal).

It does not add:

- Any new data fed to our aggregation engine.
- Any new API capability.
- Any new private-key requirement beyond the validator key itself
  (operational keys are separate).

If we never promoted past archival, the price API would work
identically. Validator is a **network contribution**, not a
serving requirement.

---

## 8. Risks + mitigations

| Risk | Phase | Mitigation |
| ---- | ----- | ---------- |
| HSM procurement delay | B | buy spare HSMs in Week 1 to avoid critical-path dependency |
| Validator key compromise | B–F | HSM-only, ceremonies logged, rotation procedure rehearsed |
| Hash divergence between our 3 archives | D+ | cross-check job, P1 alert, corrective re-mirror |
| SDF denies T1 listing | E | not service-breaking; we keep running 3 validators, re-apply in 90 days |
| Bad protocol upgrade | anytime | follow SDF's "3-of-4" upgrade rhythm; emergency rollback documented |
| Regional outage during Phase C–D | C/D | we're running 1 active region; outages degrade to "offline" without split-brain because there's only one cluster member |
| @ash unavailable for ceremony | B–D | @alex + one external witness can stand in; ceremony procedure is documented |

---

## 9. ADR reminder

This rollout plan sits within ADR-0004's commitment (three Tier-1
validators post-launch). If the plan changes — specifically, if we
decide to **never** promote to a full T1 org (e.g. operational
burden exceeds benefit) — that requires a **superseding ADR**.
This doc alone cannot downgrade ADR-0004.

---

## 10. Launch-day definition of done

- [ ] Phase A complete (1 archival node syncing + ingesting).
- [ ] Phase B complete (1 validator voting) — **can go to public
      launch without this, purely operational decision.**
- [ ] Phases C–D queued with hardware + colo contracts signed.
- [ ] Runbooks for HSM failure + key rotation + quorum-set change
      reviewed.
- [ ] Monitoring covers archive cross-check + per-validator
      correctness.

Launch can happen at the end of Phase A with pure-archival status.
Phases B–F happen in the 3 months after launch. We are not
required to be a T1 org on the launch date.
