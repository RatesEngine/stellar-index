# Exclusions and Assumptions Register

| ID | Item | Reason | Temporary/Permanent | Re-entry Evidence Needed |
| --- | --- | --- | --- | --- |
| EX-1201 | `docs/audit-2026-04-29/*` | Prior audit control artifacts are comparison inputs, not product code under this audit snapshot | temporary for this audit | New audit specifically targeting prior audit artifact integrity |
| EX-1202 | `docs/audit-2026-05-02/*` | Prior audit control artifacts; same rationale as EX-1201 | temporary for this audit | Same as EX-1201 |
| EX-1203 | `docs/audit-2026-05-12/*` (this directory) | Current audit workspace is a control plane created during the audit, not part of the pre-audit product snapshot | temporary for this audit | Separate review of audit artifact quality if needed |
| EX-1204 | `.discovery-repos/*` (29 cloned upstream projects) | Read-only third-party reference checkouts; never imported by product code (verify in W04). Their internal correctness is upstream responsibility | permanent (per audit) | A new audit specifically targeting whether discovery checkouts are accidentally read at runtime |
| EX-1205 | Hosted GitHub branch protection, required-checks, secret values | Local checkout cannot prove hosted settings without GitHub API access; record live probe transcripts when GitHub admin token is available | temporary | `gh api repos/:owner/:repo/branches/main/protection` + secrets list |
| EX-1206 | Cloudflare Pages live deploy state, page rules, cache config | Out-of-repo control plane | temporary | Cloudflare API probe or operator-captured screenshot transcript |
| EX-1207 | GHCR image retention, signing, SBOM artifacts | GitHub Container Registry settings | temporary | `gh api orgs/<org>/packages` probe + image manifest pull |
| EX-1208 | R2 (AWS) and R3 (Vultr) live state | Not deployed today; only docs (`r2-deployment-state.md`, `r3-deployment-state.md`) describe them. The docs themselves remain in audit scope under W23 | permanent for live-state | Future deployment + SSH access |
| EX-1209 | Healthchecks.io project state, ping history, channel routing | Out-of-repo SaaS control | temporary | Healthchecks.io API probe + operator dashboard capture |
| EX-1210 | Cloudflare WAF / rate-limit rules / firewall lists | Out-of-repo control | temporary | Cloudflare API + screenshot transcript |
| EX-1211 | `web/dashboard/node_modules/`, `web/explorer/node_modules/`, `web/status/node_modules/` | Resolved dependencies; covered indirectly via pnpm-lock auditing in W04 / W17 | permanent | n/a — pnpm lockfiles are the audit unit |
| EX-1212 | `web/explorer/out/`, `web/dashboard/out/`, `web/status/out/` (Next.js build artifacts) | Build outputs; truth is in source under `web/*/src/` and `next.config.mjs` | permanent | n/a |
| EX-1213 | Pre-built binaries at repo root (`ratesengine-api`, `ratesengine-aggregator`, `ratesengine-indexer`) | Local-build residue; truth is the source under `cmd/*` + Docker base images | permanent (with finding) | A separate finding will note that these binaries are tracked in worktree but `.gitignore`'d — verify correct |
| EX-1214 | Stripe / billing-provider live merchant state | Out-of-repo SaaS; covered via `internal/platform/billing.go` interface in W19 | temporary | Stripe dashboard API + operator capture |
| EX-1215 | Resend / mail-provider state | Out-of-repo SaaS; `internal/notify/resend.go` covers the contract | temporary | Resend API probe |
| EX-1216 | `configs/ansible/inventory/r1.secrets.yml` *content* | The vault file is encrypted with the operator's passphrase; the audit does not have the passphrase and will not decrypt secrets. The *existence* of the file and the `.gitignore` posture remain in scope (see F-1207) | permanent (per audit) | Vault passphrase + operator confirmation, captured securely |
| EX-1217 | `docs/audit-2026-05-12-codex/` (parallel audit workspace) | Independent control workspace for the same commit. Cross-comparison happens via R13 reconciliation; the codex workspace's *internal* quality is not in scope here | permanent (per audit) | A separate audit specifically targeting parallel-audit quality if needed |

## Re-entry Procedure

To re-enter an excluded scope:

1. Capture the re-entry evidence (column 5 above).
2. Append a new evidence row in `evidence/log.md` with the
   captured proof.
3. Open a follow-up workstream item or amend the relevant
   workstream sub-plan.
4. Move the excluded item to a **finding** if the new evidence
   reveals an issue.
