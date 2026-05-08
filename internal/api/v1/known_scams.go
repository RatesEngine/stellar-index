package v1

// knownScams flags G-strkeys that stellar.expert's directory marks
// as malicious, scam, or unsafe. Until we wire a runtime fetch
// against api.stellar.expert/explorer/public/directory/<g> (which
// would couple our API to a third-party uptime), this hand-curated
// list catches the high-volume offenders that show up in
// /v1/issuers and /v1/coins listings — issuers we'd otherwise
// surface to clients without a warning.
//
// Sourcing rule: a G-strkey is added here ONLY when stellar.expert's
// directory entry includes a `tags` array containing "malicious"
// OR "unsafe". The `Reason` field carries the directory's `name`
// when present; otherwise a one-line justification.
//
// To verify an entry: `curl https://api.stellar.expert/explorer/
// public/directory/<g>` and confirm the `tags` array.
//
// Adding a new entry requires a sibling note in the issuer-tagging
// runbook (docs/operations/runbooks/scam-issuers.md, future). For
// now: add the entry, cite the stellar.expert lookup, ship it.
type scamFlag struct {
	Reason string // human-readable label rendered in the warning badge
}

// scamIssuers is the curated set. Keep alphabetised by G-strkey.
var scamIssuers = map[string]scamFlag{
	// stellar.expert directory: name="Serial Minter / Deceptive
	// Assets", tags=["malicious","unsafe"]. Verified 2026-05-08.
	"GBLLDENR2WOOI5VV2EY2WIN6P3H2DJBDB6G45QM5PAPROQ4TFGHFBLCK": {
		Reason: "Serial Minter / Deceptive Assets (stellar.expert)",
	},
	// stellar.expert directory: name="Deprecated",
	// tags=["unsafe"]. Verified 2026-05-08.
	"GBNLJIYH34UWO5YZFA3A3HD3N76R6DOI33N4JONUOHEEYZYCAYTEJ5AK": {
		Reason: "Deprecated issuer (stellar.expert)",
	},
	// stellar.expert directory: name="SCAM Counterfeiter",
	// tags=["malicious","unsafe"]. ~4.8M observations on prod;
	// the trailing "GUARD" vanity suggests intentional brand-
	// confusion with Hashguard. Verified 2026-05-08.
	"GBYBVWOOVC4EJVRIF4HMWG5B7POLCS7JRPY5KYR3BCLEK24IJQOGUARD": {
		Reason: "SCAM Counterfeiter (stellar.expert)",
	},
	// stellar.expert directory: name="serial Scam Counterfeiter",
	// tags=["malicious"]. 472 issued asset codes, all spam-style
	// counterfeits. Verified 2026-05-08.
	"GDEUQ2MX3YXMITFOTC3CO3GW5V3XE3IVG7JKLZZAOZ7WFYIN256INDUS": {
		Reason: "Serial Scam Counterfeiter (stellar.expert)",
	},
}

// scamReason returns the curated scam reason for a G-strkey, or
// "" when the issuer isn't flagged. Callers add the warning badge
// when the result is non-empty.
func scamReason(gStrkey string) string {
	if entry, ok := scamIssuers[gStrkey]; ok {
		return entry.Reason
	}
	return ""
}
