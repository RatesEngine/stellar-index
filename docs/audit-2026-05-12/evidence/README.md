# Evidence

This directory holds the audit's evidence ledgers (split by
class per protocol §4) and supporting artifacts.

## Ledger files (one per ID prefix)

| Ledger | Prefix | Contents |
| --- | --- | --- |
| [log.md](log.md) | `EV-####` | general code/test/runtime observations |
| [commands.md](commands.md) | `CMD-####` | shell command transcripts |
| [cross-file-interactions.md](cross-file-interactions.md) | `XFI-####` | material seams between files/packages |
| [tree-observations.md](tree-observations.md) | `T-####` | tree-shape observations (size, churn, drift) |

## Sub-directories

- [r1-probes/](r1-probes/) — `R1-####` live SSH probe
  transcripts; one file per probe topic per date.
- [journeys/](journeys/) — supporting evidence files for
  end-to-end journey traces. Trace files themselves
  (`J-####` IDs) live in `../journeys-traces/`.
- [workstreams/](workstreams/) — per-workstream supporting
  evidence (deeper notes too long for a single log row).

## How to add evidence

1. Pick the next free `EV-####` ID (zero-padded, monotonic).
2. Add a row to [log.md](log.md) with the fields per
   [02-protocol.md](../02-protocol.md) §4.
3. If the evidence comes from a probe transcript, also add the
   transcript file under `r1-probes/`.
4. If the evidence is multi-paragraph or contains data, drop
   the data file under `workstreams/W##-<topic>.md` or
   `journeys/J##-<topic>.md` and reference its path from the
   log row.

Never delete an evidence row. Supersede with a new row if
needed (`superseded by EV-YYYY`).
