# Tree Observations

| ID | Observation | Evidence | Notes |
| --- | --- | --- | --- |
| T-0001 | The repo snapshot remains materially larger than the April audit baseline: 1223 tracked files, 567 Go files, 275 test files, 71 Go packages, 16 source-family directories, 6 binaries | [inventory/repo-snapshot.md](/Users/ash/code/ratesengine/docs/audit-2026-05-02/inventory/repo-snapshot.md:1) | Increases the need for unit-based inventory handling |
| T-0002 | Product scope is centered on `cmd/*`, `internal/*`, `deploy/*`, `configs/*`, and active `docs/*`; current and prior audit workspaces are control artifacts, not runtime code | [inventory/area-counts.md](/Users/ash/code/ratesengine/docs/audit-2026-05-02/inventory/area-counts.md:1), [README.md](/Users/ash/code/ratesengine/README.md:1) | Drives exclusions for audit artifacts |
