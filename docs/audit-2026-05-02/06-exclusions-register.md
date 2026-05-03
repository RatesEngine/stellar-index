# Exclusions and Assumptions Register

| ID | Item | Reason | Temporary/Permanent | Re-entry Evidence Needed |
| --- | --- | --- | --- | --- |
| EX-0501 | `docs/audit-2026-04-29/*` | Prior audit control artifacts are comparison inputs, not product code under this audit snapshot | temporary for this audit | New audit specifically targeting prior audit artifact integrity |
| EX-0502 | `docs/audit-2026-05-02/*` | Current audit workspace is a control plane created during the audit, not part of the pre-audit product snapshot | temporary for this audit | Separate review of audit artifact quality if needed |
| EX-0503 | Hosted GitHub branch protection and remote infra state | Local checkout cannot prove hosted settings or live cloud/runtime state by itself | temporary | Live GitHub/API or infra evidence |
