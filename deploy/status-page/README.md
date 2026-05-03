# Status-page scaffold

Pre-baked configuration for `status.ratesengine.net` per
[L4.11](../../docs/architecture/launch-readiness-backlog.md) and
the operator runbook in
[`docs/operations/status-page-setup.md`](../../docs/operations/status-page-setup.md).

## What's in this directory

- **`upptimerc.example.yml`** — drop-in `.upptimerc.yml` for the
  Upptime fork. Names the surfaces, configures the public page,
  routes incident assignment.

## How to use it

The full step-by-step is in
[`docs/operations/status-page-setup.md`](../../docs/operations/status-page-setup.md).
The short version:

```sh
# 1. Fork upptime/upptime → RatesEngine/ratesengine-status
#    (via "Use this template" on github.com/upptime/upptime).

# 2. In the new repo:
cp /path/to/rates-engine/deploy/status-page/upptimerc.example.yml \
   .upptimerc.yml
# Adjust per the comments in the file.

# 3. Set repo secret GH_PAT (PAT with contents:write on the new
#    repo). Push the config to main.

# 4. Trigger the first run from the Actions tab.
```

This directory exists so the operator doesn't have to rebuild the
config from the runbook prose at cutover time. Treat
`upptimerc.example.yml` as the authoritative starting point;
diverge in the live `.upptimerc.yml` only when operational
experience demands it (different probe cadence, additional
sites, etc.).
