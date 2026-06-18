// Deterministic Math.random for the Postman-collection generator.
//
// openapi-to-postmanv2 fakes example values for response bodies and
// query/path params via json-schema-faker, which calls Math.random()
// to pick among `enum` values, string formats, array lengths, etc.
// That made every `make docs-postman` run emit a DIFFERENT collection
// (e.g. `"status": "ok"` vs `"degraded"`), defeating the script's
// stated goal of a reproducible artifact whose diff only changes when
// openapi/stellar-index.v1.yaml itself changes — which is why the
// committed collection silently drifted.
//
// docs-postman.sh preloads this file via NODE_OPTIONS=--require so the
// override lands in the converter's Node process. A fixed-seed LCG is
// plenty here: we need reproducibility, not cryptographic quality.
let state = 1337 >>> 0;
Math.random = () => {
  state = (Math.imul(state, 1103515245) + 12345) >>> 0;
  return state / 4294967296;
};
