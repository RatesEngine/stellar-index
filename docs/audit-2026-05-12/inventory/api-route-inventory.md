# API Route Inventory

Auto-extracted from `internal/api/v1/`. Reconcile each route against `openapi/rates-engine.v1.yaml` and against the live R1 surface.

| Method | Path | Source file | OpenAPI | Auth | Cache | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| | | internal/api/v1/dashboardauth/handlers.go:111 | | | | 	mux.HandleFunc("POST /v1/auth/login", h.HandleLogin) |
| | | internal/api/v1/dashboardauth/handlers.go:112 | | | | 	mux.HandleFunc("GET /v1/auth/callback", h.HandleCallback) |
| | | internal/api/v1/dashboardauth/handlers.go:113 | | | | 	mux.HandleFunc("POST /v1/auth/logout", h.HandleLogout) |
| | | internal/api/v1/dashboardauth/handlers.go:265 | | | | 		GeoFirstSeen: r.Header.Get("CF-IPCountry"), // safe to leave empty when CF isn't fronting |
| | | internal/api/v1/dashboardauth/handlers.go:266 | | | | 		GeoLastSeen:  r.Header.Get("CF-IPCountry"), |
| | | internal/api/v1/middleware/cors.go:73 | | | | 			origin := r.Header.Get("Origin") |
| | | internal/api/v1/middleware/cors.go:100 | | | | 			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" { |
| | | internal/api/v1/middleware/auth.go:244 | | | | 	return strings.TrimSpace(r.Header.Get("X-API-Key")) |
| | | internal/api/v1/middleware/auth.go:251 | | | | 	h := r.Header.Get("Authorization") |
| | | internal/api/v1/middleware/auth.go:273 | | | | 	ua := r.Header.Get("User-Agent") |
| | | internal/api/v1/middleware/request_id.go:31 | | | | 		id := r.Header.Get(HeaderRequestID) |
| | | internal/api/v1/middleware/remoteip.go:47 | | | | 	if client := firstForwardedFor(r.Header.Get("X-Forwarded-For")); client != "" { |
| | | internal/api/v1/stripe_webhook.go:246 | | | | 	sigHeader := r.Header.Get("Stripe-Signature") |
| | | internal/api/v1/server.go:693 | | | | 	s.mux.HandleFunc("GET /v1/issuers", s.handleIssuersList) |
| | | internal/api/v1/server.go:694 | | | | 	s.mux.HandleFunc("GET /v1/issuers/{g_strkey}", s.handleIssuer) |
| | | internal/api/v1/server.go:695 | | | | 	s.mux.HandleFunc("GET /v1/changes/{entity_type}/{id}", s.handleChangeSummary) |
| | | internal/api/v1/server.go:696 | | | | 	s.mux.HandleFunc("GET /v1/diagnostics/cursors", s.handleCursors) |
| | | internal/api/v1/server.go:697 | | | | 	s.mux.HandleFunc("GET /v1/incidents", s.handleIncidents) |
| | | internal/api/v1/server.go:698 | | | | 	s.mux.HandleFunc("GET /v1/incidents.atom", s.handleIncidentsAtom) |
| | | internal/api/v1/server.go:699 | | | | 	s.mux.HandleFunc("GET /v1/network/stats", s.handleNetworkStats) |
| | | internal/api/v1/server.go:700 | | | | 	s.mux.HandleFunc("GET /v1/healthz", s.handleHealthz) |
| | | internal/api/v1/server.go:701 | | | | 	s.mux.HandleFunc("GET /v1/readyz", s.handleReadyz) |
| | | internal/api/v1/server.go:702 | | | | 	s.mux.HandleFunc("GET /v1/version", s.handleVersion) |
| | | internal/api/v1/server.go:703 | | | | 	s.mux.HandleFunc("GET /v1/status", s.handleStatus) |
| | | internal/api/v1/server.go:719 | | | | 	s.mux.Handle("GET /metrics", loopbackOnly(obs.Handler())) |
| | | internal/api/v1/server.go:722 | | | | 	s.mux.HandleFunc("GET /v1/assets", s.handleAssetList) |
| | | internal/api/v1/server.go:727 | | | | 	s.mux.HandleFunc("GET /v1/assets/verified", s.handleAssetsVerified) |
| | | internal/api/v1/server.go:728 | | | | 	s.mux.HandleFunc("GET /v1/assets/{asset_id}", s.handleAssetGet) |
| | | internal/api/v1/server.go:733 | | | | 	s.mux.HandleFunc("GET /v1/assets/{asset_id}/metadata", s.handleAssetMetadata) |
| | | internal/api/v1/server.go:739 | | | | 	s.mux.HandleFunc("GET /v1/assets/{asset_id}/{network}", s.handleAssetByNetwork) |
| | | internal/api/v1/server.go:743 | | | | 	s.mux.HandleFunc("GET /v1/price", s.handlePrice) |
| | | internal/api/v1/server.go:748 | | | | 	s.mux.HandleFunc("GET /v1/price/tip", s.handlePriceTip) |
| | | internal/api/v1/server.go:753 | | | | 	s.mux.HandleFunc("GET /v1/price/tip/stream", s.handlePriceTipStream) |
| | | internal/api/v1/server.go:758 | | | | 	s.mux.HandleFunc("GET /v1/observations", s.handleObservations) |
| | | internal/api/v1/server.go:762 | | | | 	s.mux.HandleFunc("GET /v1/observations/stream", s.handleObservationsStream) |
| | | internal/api/v1/server.go:767 | | | | 	s.mux.HandleFunc("GET /v1/price/stream", s.handlePriceStream) |
| | | internal/api/v1/server.go:770 | | | | 	s.mux.HandleFunc("GET /v1/price/batch", s.handlePriceBatch) |
| | | internal/api/v1/server.go:774 | | | | 	s.mux.HandleFunc("POST /v1/price/batch", s.handlePriceBatchPost) |
| | | internal/api/v1/server.go:777 | | | | 	s.mux.HandleFunc("GET /v1/history", s.handleHistory) |
| | | internal/api/v1/server.go:782 | | | | 	s.mux.HandleFunc("GET /v1/history/since-inception", s.handleHistorySinceInception) |
| | | internal/api/v1/server.go:786 | | | | 	s.mux.HandleFunc("GET /v1/chart", s.handleChart) |
| | | internal/api/v1/server.go:789 | | | | 	s.mux.HandleFunc("GET /v1/ohlc", s.handleOHLC) |
| | | internal/api/v1/server.go:792 | | | | 	s.mux.HandleFunc("GET /v1/vwap", s.handleVWAP) |
| | | internal/api/v1/server.go:795 | | | | 	s.mux.HandleFunc("GET /v1/twap", s.handleTWAP) |
| | | internal/api/v1/server.go:798 | | | | 	s.mux.HandleFunc("GET /v1/markets", s.handleMarkets) |
| | | internal/api/v1/server.go:802 | | | | 	s.mux.HandleFunc("GET /v1/pools", s.handlePools) |
| | | internal/api/v1/server.go:805 | | | | 	s.mux.HandleFunc("GET /v1/pairs", s.handlePairs) |
| | | internal/api/v1/server.go:808 | | | | 	s.mux.HandleFunc("GET /v1/oracle/latest", s.handleOracleLatest) |
| | | internal/api/v1/server.go:813 | | | | 	s.mux.HandleFunc("GET /v1/oracle/streams", s.handleOracleStreams) |
| | | internal/api/v1/server.go:819 | | | | 	s.mux.HandleFunc("GET /v1/oracle/lastprice", s.handleOracleLastPrice) |
| | | internal/api/v1/server.go:820 | | | | 	s.mux.HandleFunc("GET /v1/oracle/prices", s.handleOraclePrices) |
| | | internal/api/v1/server.go:821 | | | | 	s.mux.HandleFunc("GET /v1/oracle/x_last_price", s.handleOracleXLastPrice) |
| | | internal/api/v1/server.go:824 | | | | 	s.mux.HandleFunc("GET /v1/lending/pools", s.handleLendingPools) |
| | | internal/api/v1/server.go:828 | | | | 	s.mux.HandleFunc("GET /v1/sources", s.handleSources) |
| | | internal/api/v1/server.go:835 | | | | 	s.mux.HandleFunc("GET /v1/methodology", s.handleMethodology) |
| | | internal/api/v1/server.go:842 | | | | 	s.mux.HandleFunc("GET /v1/sac-wrappers", s.handleSACWrappers) |
| | | internal/api/v1/server.go:848 | | | | 	s.mux.HandleFunc("GET /v1/account/me", s.handleAccountMe) |
| | | internal/api/v1/server.go:849 | | | | 	s.mux.HandleFunc("GET /v1/account/usage", s.handleAccountUsage) |
| | | internal/api/v1/server.go:850 | | | | 	s.mux.HandleFunc("GET /v1/account/keys", s.handleAccountKeysList) |
| | | internal/api/v1/server.go:851 | | | | 	s.mux.HandleFunc("POST /v1/account/keys", s.handleAccountKeysCreate) |
| | | internal/api/v1/server.go:852 | | | | 	s.mux.HandleFunc("DELETE /v1/account/keys/{keyID}", s.handleAccountKeysRevoke) |
| | | internal/api/v1/server.go:853 | | | | 	s.mux.HandleFunc("POST /v1/signup", s.handleSignup) |
| | | internal/api/v1/server.go:854 | | | | 	s.mux.HandleFunc("POST /v1/webhooks/stripe", s.handleStripeWebhook) |
| | | internal/api/v1/server.go:877 | | | | 	s.mux.HandleFunc("GET /v1/auth/sep10/challenge", s.handleSEP10Challenge) |
| | | internal/api/v1/server.go:878 | | | | 	s.mux.HandleFunc("POST /v1/auth/sep10/token", s.handleSEP10Token) |
| | | internal/api/v1/server.go:888 | | | | 	s.mux.HandleFunc("GET /{$}", s.handleRoot) |
| | | internal/api/v1/server.go:900 | | | | 	s.mux.HandleFunc("GET /robots.txt", s.handleRobotsTxt) |
| | | internal/api/v1/server.go:907 | | | | 	s.mux.HandleFunc("GET /.well-known/security.txt", s.handleSecurityTxt) |
| | | internal/api/v1/assets_global_test.go:339 | | | | 	loc := resp.Header.Get("Location") |
| | | internal/api/v1/dashboardkeys/handlers.go:103 | | | | 	mux.HandleFunc("GET /v1/dashboard/keys", h.HandleList) |
| | | internal/api/v1/dashboardkeys/handlers.go:104 | | | | 	mux.HandleFunc("POST /v1/dashboard/keys", h.HandleCreate) |
| | | internal/api/v1/dashboardkeys/handlers.go:105 | | | | 	mux.HandleFunc("DELETE /v1/dashboard/keys/{id}", h.HandleRevoke) |
| | | internal/api/v1/assets_test.go:202 | | | | 	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" { |
| | | internal/api/v1/envelope_test.go:31 | | | | 	if got := res.Header.Get("Content-Type"); got != "application/json" { |
| | | internal/api/v1/account_test.go:109 | | | | 	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") { |
| | | internal/api/v1/handler_middleware_test.go:47 | | | | 	if resp.Header.Get("X-Test-MW") != "cors" { |
| | | internal/api/v1/handler_middleware_test.go:48 | | | | 		t.Errorf("expected X-Test-MW=cors, got %q", resp.Header.Get("X-Test-MW")) |
| | | internal/api/v1/server_test.go:58 | | | | 	if ct := resp.Header.Get("Content-Type"); ct != "application/json" { |
| | | internal/api/v1/server_test.go:219 | | | | 	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" { |
| | | internal/api/v1/server_test.go:252 | | | | 	if got := resp.Header.Get("Cache-Control"); got != "no-store" { |
| | | internal/api/v1/server_test.go:268 | | | | 	if got := resp.Header.Get("Cache-Control"); got != "no-store" { |
| | | internal/api/v1/server_test.go:283 | | | | 	if ct := resp.Header.Get("Content-Type"); ct != "application/json" { |
| | | internal/api/v1/server_test.go:314 | | | | 	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" { |
| | | internal/api/v1/server_test.go:348 | | | | 	if got := resp.Header.Get("X-Request-ID"); got != "test-trace-abc" { |
| | | internal/api/v1/server_test.go:358 | | | | 	if id := resp2.Header.Get("X-Request-ID"); len(id) != 32 { |
| | | internal/api/v1/server_test.go:407 | | | | 	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" { |
| | | internal/api/v1/server_test.go:500 | | | | 	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" { |
| | | internal/api/streaming/handler.go:128 | | | | 	if v := r.Header.Get("Last-Event-ID"); v != "" { |
| | | internal/api/streaming/handler_test.go:74 | | | | 	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" { |

_Reviewer: dedupe, fill Method/Path columns from the source code, and add a row in `04-reconciliation.md` R04 for every route._
