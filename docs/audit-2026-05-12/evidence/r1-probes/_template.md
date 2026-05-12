# R1 Probe — &lt;topic&gt; — &lt;YYYY-MM-DD&gt;

Topic: <e.g. "service inventory", "Redis health", "MinIO disk pressure">
Probe section: <§ from `12-r1-live-probe-protocol.md`>
UTC timestamp: <ISO-8601>
Audit context: <claim or hypothesis being tested>

## Commands

```sh
ssh -o ConnectTimeout=8 root@136.243.90.96 '<exact command, multi-line ok>'
```

## Output

```text
<paste raw, scrubbed of secrets, truncated to relevant lines>
```

## Findings

- Observation 1: …
- Observation 2: …

## Disposition

- `EV-####` evidence row added to `../log.md`
- `F-####` finding(s) opened (if applicable)
- Cross-ref to workstream sub-plan: `../../workstreams/W##-…md`
