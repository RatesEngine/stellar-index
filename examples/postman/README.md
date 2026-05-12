# Postman / Insomnia / Bruno collection

`rates-engine.postman_collection.json` is a Postman v2.1 collection
auto-generated from
[`openapi/rates-engine.v1.yaml`](../../openapi/rates-engine.v1.yaml).
The OpenAPI spec is the source of truth — regenerate after any
spec change with:

```sh
make docs-postman
```

This is the customer-facing canonical path. The docs-site build
pipeline (docs.ratesengine.net) regenerates its own copy at
build time; nothing else in the repo writes to this file.

## Importing

| Tool | How |
|------|-----|
| Postman | File → Import → drop the JSON file |
| Insomnia | Application menu → Import/Export → Import Data → From File |
| Bruno | File → Open Collection → select the JSON file |

## Setting variables

The collection ships with two collection-level variables:

- `baseUrl` — defaults to `https://api.ratesengine.net`. Override
  to hit a local indexer (`http://localhost:3000`).
- `bearerToken` — your API key plaintext. Required only for
  `/v1/account/*`, `/v1/account/keys`, and any other authed
  endpoint. Get one by running the `POST /v1/signup` request in
  the collection first.

In Postman: Collections → Rates Engine API → Variables tab.
