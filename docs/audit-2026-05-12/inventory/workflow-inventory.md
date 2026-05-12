# CI/CD Workflow Inventory

| Workflow | Triggers | Jobs | Notes |
| --- | --- | --- | --- |
| `api-audit.yml` | on:,  push:,  workflow_dispatch: | on: permissions: jobs:  | todo |
| `api-docs.yml` | on:,  workflow_dispatch: | on: permissions: concurrency: jobs:  | todo |
| `ci.yml` | on:,  pull_request: | on: env: concurrency: jobs:  | todo |
| `deploy.yml` | on:,  workflow_dispatch: | on: concurrency: permissions: jobs:  | todo |
| `docs-deploy.yml` | on:,  workflow_dispatch: | on: permissions: concurrency: jobs:  | todo |
| `explorer-deploy.yml` | on:,  workflow_dispatch: | on: permissions: concurrency: jobs:  | todo |
| `k6-weekly.yml` | on:,  workflow_dispatch: | on: permissions: jobs:  | todo |
| `release-validate.yml` | on:,  pull_request:,  workflow_dispatch: | on: concurrency: permissions: jobs:  | todo |
| `release.yml` | on:,  push:,  workflow_dispatch:,  release: | on: concurrency: permissions: jobs:  | todo |
| `status-page.yml` | on:,  workflow_dispatch: | on: permissions: concurrency: jobs:  | todo |
