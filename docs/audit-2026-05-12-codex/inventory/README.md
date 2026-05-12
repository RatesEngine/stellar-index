# Inventory

The tracked-file inventory is generated from `git ls-files`. It is the
mandatory per-file audit queue.

Files:

- [file-coverage.tsv](file-coverage.tsv): one row per tracked file
  before this audit directory was added
- [area-counts.md](area-counts.md): tracked file counts by top-level area
- [repo-snapshot.md](repo-snapshot.md): snapshot metadata
- [generate.sh](generate.sh): regeneration helper

Ignored build outputs, local caches, `.discovery-repos/`, and live R1
state are not tracked application files. They are covered by specific
workstreams when they affect provenance, generation, deployment, or
runtime truth.
