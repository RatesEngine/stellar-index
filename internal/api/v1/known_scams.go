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
//
// Each entry is sourced by sweeping the top observation-count
// uncurated G-strkeys (no home_domain in /v1/issuers) against
// api.stellar.expert/explorer/public/directory/<g> and adding any
// row whose tags include "malicious" or "unsafe". The Reason
// captures the directory's `name` field. All verified 2026-05-08.
var scamIssuers = map[string]scamFlag{
	// "Scam Assets" — malicious, unsafe.
	"GA2XZLXNLAL26VBCA2OESAIMXTRH5GXKLHYZMDGNCR2SYS5QZWWNBLCK": {
		Reason: "Scam Assets (stellar.expert)",
	},
	// "Scam Assets" — malicious, unsafe.
	"GA6I723V2YMFP7L3H5XFTYIF4X5AJPPVKEK2EZHLFKEIDZ7FW775BLCK": {
		Reason: "Scam Assets (stellar.expert)",
	},
	// "SwissCustody Scam" — malicious.
	"GAME5YNKHIPRSMN2BI3HLS3OYD4NE6FQ2WKUUJPYYM6GU4MRDUBPHN6B": {
		Reason: "SwissCustody Scam (stellar.expert)",
	},
	// "Scam" (generic counterfeiter) — malicious.
	"GAUCPLSPBJKOSN7WZK6SDD2BYPQMC3YSWMLX4XXY7S4JPQFLJXEINDUS": {
		Reason: "Scam (stellar.expert)",
	},
	// "SCAM-Counterfeiter" — malicious, unsafe.
	"GB2Z42DRDOLLRLH62JFM3EC35DHD2KPETP6KKMX7EYKLFZEUXGGXG7L6": {
		Reason: "SCAM Counterfeiter (stellar.expert)",
	},
	// "Serial Minter / Fake Assets" — malicious, unsafe.
	"GBM5MM2BQ5IK6F2DTPDV4EMTS4OMYFXKUOB7IV5JLJ4MGQ44LJFEUXLM": {
		Reason: "Serial Minter / Fake Assets (stellar.expert)",
	},
	// "Scam" — malicious, unsafe.
	"GBMHWBIT37IQSVUXJHTWMFYBO32EU7Q53UPWGDL5HLI3QFJTCN5CUXLM": {
		Reason: "Scam (stellar.expert)",
	},
	// "Scam" — malicious, unsafe.
	"GBFSPDXQ4YCDNDGGPJDXKMCQZ7ND6CTVLLPQ6BTJVFBAZQFVWCRYQNTM": {
		Reason: "Scam (stellar.expert)",
	},
	// "Serial Minter / Deceptive Assets" — malicious, unsafe.
	"GBLLDENR2WOOI5VV2EY2WIN6P3H2DJBDB6G45QM5PAPROQ4TFGHFBLCK": {
		Reason: "Serial Minter / Deceptive Assets (stellar.expert)",
	},
	// "Deprecated" issuer — unsafe.
	"GBNLJIYH34UWO5YZFA3A3HD3N76R6DOI33N4JONUOHEEYZYCAYTEJ5AK": {
		Reason: "Deprecated issuer (stellar.expert)",
	},
	// "SCAM Counterfeiter" — malicious, unsafe. ~4.8M observations
	// on prod; the trailing "GUARD" vanity suggests intentional
	// brand-confusion with Hashguard.
	"GBYBVWOOVC4EJVRIF4HMWG5B7POLCS7JRPY5KYR3BCLEK24IJQOGUARD": {
		Reason: "SCAM Counterfeiter (stellar.expert)",
	},
	// "Deceptive Asset" — malicious, unsafe.
	"GCCHXGXLTVNIHWUWXWMBSWN2C2SI4K2JLS5OIZBELNC2U6H363T5BLCK": {
		Reason: "Deceptive Asset (stellar.expert)",
	},
	// "InterstellarExchange" — flagged as unsafe.
	"GCNSGHUCG5VMGLT5RIYYZSO7VQULQKAJ62QA33DBC5PPBSO57LFWVV6P": {
		Reason: "InterstellarExchange (unsafe per stellar.expert)",
	},
	// "Scam" — malicious.
	"GC7NQHBFYMXLFFUAYDTGCVVJ7YEH4LTCUELK3P5B6IJTNZ7S7ALBMDZJ": {
		Reason: "Scam (stellar.expert)",
	},
	// "Scam" — malicious, unsafe.
	"GC77GLL7ZBRJOM764JGOSMDZKGSEWRIXKUCOZREJLMAH7JGLIRW4WOLF": {
		Reason: "Scam (stellar.expert)",
	},
	// "Scam" — malicious, unsafe.
	"GCU5BRVNVXJ75RLJY466HQFHU5YYCZJOK7MDHS2BELE35ZIXDVKXINDS": {
		Reason: "Scam (stellar.expert)",
	},
	// "Serial Minter / Deceptive Assets" — malicious, unsafe.
	"GCUG7ARUFEEUMSL56K7245YCPXPZPOXAY6TSRXZB2JZFBI4DOBVOTUSA": {
		Reason: "Serial Minter / Deceptive Assets (stellar.expert)",
	},
	// "serial Scam Counterfeiter" — malicious. 472 issued asset
	// codes, all spam-style counterfeits.
	"GDEUQ2MX3YXMITFOTC3CO3GW5V3XE3IVG7JKLZZAOZ7WFYIN256INDUS": {
		Reason: "Serial Scam Counterfeiter (stellar.expert)",
	},
	// "Scam" — malicious.
	"GDOEVDDBU6OBWKL7VHDAOKD77UP4DKHQYKOKJJT5PR3WRDBTX35HUEUX": {
		Reason: "Scam (stellar.expert)",
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
