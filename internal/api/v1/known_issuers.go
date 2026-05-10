package v1

// knownIssuers is a hand-curated fallback map from issuer
// G-strkey to (home_domain, org_name). The production
// `issuers.home_domain` column stays empty until an issuer-upsert
// path lands that propagates from `account_observations` —
// without that, every /v1/issuers row renders home_domain=null
// and the explorer shows just a truncated G-strkey.
//
// Until that pipeline lands, fall back to this map at the wire
// boundary so the top issuers (USDC, AQUA, yXLM, SHX, …) render
// with readable names. Each entry is sourced from the issuer's
// public stellar.toml at the cited domain — operator can
// re-verify with `curl https://<domain>/.well-known/stellar.toml`.
//
// To add an issuer: append a new entry below. Do NOT add an
// entry without first verifying the G-strkey controls the
// home_domain (e.g. via a stellar.toml ACCOUNTS array
// listing the G-account). A wrong mapping is worse than a null.
//
// Long-term path: PR that wires `issuers` table writes from the
// AccountEntry observer (see task #95-adjacent investigation).
// Once that's in place, this map becomes redundant and can be
// removed.
type knownIssuer struct {
	HomeDomain string
	OrgName    string
}

var knownIssuers = map[string]knownIssuer{
	// Circle — USDC. Verified via centre.io/.well-known/stellar.toml.
	"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN": {
		HomeDomain: "centre.io",
		OrgName:    "Circle",
	},
	// Ultra Capital — yXLM, yUSDC.
	"GARDNV3Q7YGT4AKSDF25LT32YSCCW4EV22Y2TV3I2PU2MMXJTEDL5T55": {
		HomeDomain: "ultracapital.xyz",
		OrgName:    "Ultra Capital",
	},
	// Aquarius — AQUA governance token.
	"GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA": {
		HomeDomain: "aqua.network",
		OrgName:    "Aquarius",
	},
	// Stronghold — SHX.
	"GDSTRSHXHGJ7ZIVRBXEYE5Q74XUVCUSEKEBR7UCHEUUEK72N7I7KJ6JH": {
		HomeDomain: "stronghold.co",
		OrgName:    "Stronghold",
	},
	// MoneyGram — international remittance USDC.
	"GASD3HGFYGNNHTJVUZAYFRNPHIZHTBSCCN4TQYTQR3MOIIH4KOLLOWMD": {
		HomeDomain: "stellar.moneygram.com",
		OrgName:    "MoneyGram International",
	},
	// AnchorUSD.
	"GDUKMGUGDZQK6YHYA5Z6AY2G4XDSZPSZ3SW5UN3ARVMO6QSRDWP5YLEX": {
		HomeDomain: "anchorusd.com",
		OrgName:    "AnchorUSD",
	},
	// Round 2 (2026-05-08): issuers identified via the SAC wrapper
	// rounds — every entry verified by cross-referencing the
	// G-strkey against the issuer's stellar.toml ACCOUNTS list.
	// Blend Capital — BLND governance token.
	"GDJEHTBE6ZHUXSWFI642DCGLUOECLHPF3KSXHPXTSTJ7E3JF6MQ5EZYY": {
		HomeDomain: "blend.capital",
		OrgName:    "Blend Capital",
	},
	// Velo Labs — VELO.
	"GDM4RQUQQUVSKQA7S6EM7XBZP3FCGH4Q7CL6TABQ7B2BEJ5ERARM2M5M": {
		HomeDomain: "velo.org",
		OrgName:    "Velo Labs",
	},
	// Phoenix DEX — PHO governance token.
	"GAX5TXB5RYJNLBUR477PEXM4X75APK2PGMTN6KEFQSESGWFXEAKFSXJO": {
		HomeDomain: "phoenix-hub.io",
		OrgName:    "Phoenix",
	},
	// Mykobo — issues USDx, EURx, GBPx (multi-currency stablecoins).
	"GAVH5ZWACAY2PHPUG4FL3LHHJIYIHOFPSIUGM2KHK25CJWXHAV6QKDMN": {
		HomeDomain: "mykobo.co",
		OrgName:    "Mykobo",
	},
	// Apay — issues wrapped BTC/ETH on Stellar.
	"GDPJALI4AZKUU2W426U5WKMAT6CN3AJRPIIRYR2YM54TL2GDWO5O2MZM": {
		HomeDomain: "apay.io",
		OrgName:    "Apay",
	},
	"GBFXOHVAS43OIWNIO7XLRJAHT3BICFEIKOJLZVXNT572MISM4CMGSOCC": {
		HomeDomain: "apay.io",
		OrgName:    "Apay",
	},
	// LIBRE — Libre Capital.
	"GAYCCWKECNGDRHYU3UTREBD2XLC3CUQN6FV22TKM4WCQER3IWR7TF5CY": {
		HomeDomain: "libre.cx",
		OrgName:    "Libre",
	},
	// Circle EUR-pegged stablecoin (EURC).
	"GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2": {
		HomeDomain: "centre.io",
		OrgName:    "Circle (EURC)",
	},
	// Round 5 (2026-05-08): legitimate issuers found via wider
	// stellar.expert directory sweep. Each verified — directory
	// has a `name` and either no `tags` or only neutral tags.
	"GDGTVWSM4MGS4T7Z6W4RPWOCHE2I6RDFCIFZGS3DOA63LWQTRNZNTTFF": {
		HomeDomain: "ultracapital.xyz",
		OrgName:    "UltraCapital (yUSDC)",
	},
	"GBXRPL45NPHCVMFFAYZVUVFFVKSIZ362ZXFP7I2ETNQ3QKZMFLPRDTD5": {
		HomeDomain: "fchain.io",
		OrgName:    "Firefly",
	},
	"GAKTLPC4ZV37SSCITQ5IS5AQ4WPF4CF4VZJQPPAROSGXMYOATF5U6XPR": {
		HomeDomain: "zeam.money",
		OrgName:    "Zeam.Money",
	},
	"GAROH4EV3WVVTRQKEY43GZK3XSRBEYETRVZ7SVG5LHWOAANSMCTJBB3U": {
		HomeDomain: "zeam.money",
		OrgName:    "Zeam.Money",
	},
	"GBHFGY3ZNEJWLNO4LBUKLYOCEK4V7ENEBJGPRHHX7JU47GWHBREH37UR": {
		HomeDomain: "sl8.online",
		OrgName:    "sl8.online",
	},
	"GC6OYQJIZF3HFXCYPFCBXYXNGIBQ4TNSFUBUXQJOZWIP6F3YZK4QH3VQ": {
		HomeDomain: "scopuly.com",
		OrgName:    "Scopuly",
	},
	"GAB7STHVD5BDH3EEYXPI3OM7PCS4V443PYB5FNT6CFGJVPDLMKDM24WK": {
		HomeDomain: "lumenswap.io",
		OrgName:    "Lumenswap",
	},
	"GC4HS4CQCZULIOTGLLPGRAAMSBDLFRR6Y7HCUQG66LNQDISXKIXXADIM": {
		HomeDomain: "ixinium.io",
		OrgName:    "Ixinium",
	},
	"GBCB4WO6J4ET55RWK2SVX76LUQ4PQ7TCDHG2YFILQML7D6XR3HACLXAU": {
		HomeDomain: "xau.cl",
		OrgName:    "XAU CL",
	},
	"GAORYJ3KBDGIM7FFSKVUJHJ5NEFWIRDIAGGBJBJS7TY6ECZS53257IG4": {
		HomeDomain: "dogstarcoin.com",
		OrgName:    "Dogstarcoin",
	},
	"GA6HCMBLTZS5VYYBCATRBRZ3BZJMAFUDKYYF6AH6MVCMGWMRDNSWJPIH": {
		HomeDomain: "mobius.network",
		OrgName:    "Mobius",
	},
	"GDNUVPUOMWOF2ML5FA5E4HQDX7EHV3VCJTLLTO563PUMZKMHJUJIJSYI": {
		HomeDomain: "afreum.com",
		OrgName:    "Afreum",
	},
	"GALLBRBQHAPW5FOVXXHYWR6J4ZDAQ35BMSNADYGBW25VOUHUYRZM4XIL": {
		HomeDomain: "allbridge.io",
		OrgName:    "Allbridge",
	},
}

// enrichIssuer fills empty home_domain / org_name fields on the
// passed entry with the curated fallback when one exists. Returns
// the (possibly mutated) values. Pass-through when the DB already
// populated them — DB wins, since an operator with a real
// `ratesengine-ops sep1-refresh` cron has more current data than
// the static map.
func enrichIssuer(gStrkey, homeDomain, orgName string) (string, string) {
	if homeDomain != "" && orgName != "" {
		return homeDomain, orgName
	}
	known, ok := knownIssuers[gStrkey]
	if !ok {
		return homeDomain, orgName
	}
	if homeDomain == "" {
		homeDomain = known.HomeDomain
	}
	if orgName == "" {
		orgName = known.OrgName
	}
	return homeDomain, orgName
}
