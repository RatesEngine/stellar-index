# Rates Engine — customer dashboard

Static-export Next.js 15 app deployed to **app.ratesengine.net**
(Cloudflare Pages). Cookie-based auth backed by the
`/v1/auth/{login,callback,logout}` endpoints in
`internal/api/v1/dashboardauth/`.

## Local dev

```sh
pnpm install
pnpm dev   # http://localhost:3001
```

Talks to `https://api.ratesengine.net` by default. To point at a
local API:

```sh
NEXT_PUBLIC_API_BASE_URL=http://localhost:3000 pnpm dev
```

For the cookie flow to work cross-origin in dev, the local API
must be configured with:

- `[api].allowed_origins = ["http://localhost:3001"]`
- `[api.dashboard].cookie_secure = false`
- `[api.dashboard].base_url = "http://localhost:3001"`

## Routes

| Path        | Auth     | Purpose                                   |
| ----------- | -------- | ----------------------------------------- |
| `/`         | gate     | Bounce to `/signin/` or `/keys/`          |
| `/signin/`  | public   | Email-input form → `POST /v1/auth/login`  |
| `/keys/`    | required | API-key management (Week 4)               |
| `/usage/`   | required | Per-day usage charts (Week 5)             |
| `/settings/`| required | Profile + account                         |
| `/admin/`   | staff    | Staff cockpit (Phase 1.5)                 |

The `/auth/callback` route is **server-side** (the Go API): clicking
the magic link in the email lands at
`https://api.ratesengine.net/v1/auth/callback?token=…`, which sets
the session cookie and 303-redirects back into this SPA.
