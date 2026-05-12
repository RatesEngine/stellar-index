# Dependency Inventory

## Go modules (`go.mod` direct + indirect)

Note: this lists both direct and `// indirect` deps. The audit unit per W04 is the *direct* set + the transitive surface visible to go-mod-verify; both are tabulated together for completeness.

| Module | Version | Notes |
| --- | --- | --- |
| `github.com/BurntSushi/toml` | `v1.6.0` | todo |
| `github.com/alicebob/miniredis/v2` | `v2.37.0` | todo |
| `github.com/golang-migrate/migrate/v4` | `v4.19.1` | todo |
| `github.com/lib/pq` | `v1.12.3` | todo |
| `github.com/prometheus/client_golang` | `v1.23.2` | todo |
| `github.com/redis/go-redis/v9` | `v9.19.0` | todo |
| `github.com/testcontainers/testcontainers-go` | `v0.42.0` | todo |
| `github.com/testcontainers/testcontainers-go/modules/postgres` | `v0.42.0` | todo |
| `golang.org/x/sync` | `v0.20.0` | todo |
| `cloud.google.com/go/bigquery` | `v1.77.0` | todo |
| `github.com/coder/websocket` | `v1.8.14` | todo |
| `github.com/google/uuid` | `v1.6.0` | todo |
| `github.com/prometheus/client_model` | `v0.6.2` | todo |
| `google.golang.org/api` | `v0.278.0` | todo |
| `gopkg.in/yaml.v3` | `v3.0.1` | todo |
| `cel.dev/expr` | `v0.25.1` | todo |
| `cloud.google.com/go` | `v0.123.0` | todo |
| `cloud.google.com/go/auth` | `v0.20.0` | todo |
| `cloud.google.com/go/auth/oauth2adapt` | `v0.2.8` | todo |
| `cloud.google.com/go/compute/metadata` | `v0.9.0` | todo |
| `cloud.google.com/go/iam` | `v1.7.0` | todo |
| `cloud.google.com/go/monitoring` | `v1.24.3` | todo |
| `cloud.google.com/go/storage` | `v1.62.0` | todo |
| `dario.cat/mergo` | `v1.0.2` | todo |
| `github.com/Azure/go-ansiterm` | `v0.0.0-20250102033503-faa5f7b0171c` | todo |
| `github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp` | `v1.31.0` | todo |
| `github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric` | `v0.55.0` | todo |
| `github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping` | `v0.55.0` | todo |
| `github.com/Microsoft/go-winio` | `v0.6.2` | todo |
| `github.com/apache/arrow/go/v15` | `v15.0.2` | todo |
| `github.com/aws/aws-sdk-go` | `v1.49.6` | todo |
| `github.com/aws/aws-sdk-go-v2` | `v1.36.5` | todo |
| `github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream` | `v1.6.11` | todo |
| `github.com/aws/aws-sdk-go-v2/config` | `v1.29.17` | todo |
| `github.com/aws/aws-sdk-go-v2/credentials` | `v1.17.70` | todo |
| `github.com/aws/aws-sdk-go-v2/feature/ec2/imds` | `v1.16.32` | todo |
| `github.com/aws/aws-sdk-go-v2/feature/s3/manager` | `v1.17.83` | todo |
| `github.com/aws/aws-sdk-go-v2/internal/configsources` | `v1.3.36` | todo |
| `github.com/aws/aws-sdk-go-v2/internal/endpoints/v2` | `v2.6.36` | todo |
| `github.com/aws/aws-sdk-go-v2/internal/ini` | `v1.8.3` | todo |
| `github.com/aws/aws-sdk-go-v2/internal/v4a` | `v1.3.36` | todo |
| `github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding` | `v1.12.4` | todo |
| `github.com/aws/aws-sdk-go-v2/service/internal/checksum` | `v1.7.4` | todo |
| `github.com/aws/aws-sdk-go-v2/service/internal/presigned-url` | `v1.12.17` | todo |
| `github.com/aws/aws-sdk-go-v2/service/internal/s3shared` | `v1.18.17` | todo |
| `github.com/aws/aws-sdk-go-v2/service/s3` | `v1.83.0` | todo |
| `github.com/aws/aws-sdk-go-v2/service/sso` | `v1.25.5` | todo |
| `github.com/aws/aws-sdk-go-v2/service/ssooidc` | `v1.30.3` | todo |
| `github.com/aws/aws-sdk-go-v2/service/sts` | `v1.34.0` | todo |
| `github.com/aws/smithy-go` | `v1.22.4` | todo |
| `github.com/beorn7/perks` | `v1.0.1` | todo |
| `github.com/cenkalti/backoff/v4` | `v4.3.0` | todo |
| `github.com/cespare/xxhash/v2` | `v2.3.0` | todo |
| `github.com/cncf/xds/go` | `v0.0.0-20251210132809-ee656c7534f5` | todo |
| `github.com/containerd/errdefs` | `v1.0.0` | todo |
| `github.com/containerd/errdefs/pkg` | `v0.3.0` | todo |
| `github.com/containerd/log` | `v0.1.0` | todo |
| `github.com/containerd/platforms` | `v0.2.1` | todo |
| `github.com/cpuguy83/dockercfg` | `v0.3.2` | todo |
| `github.com/creachadair/jrpc2` | `v1.2.0` | todo |
| `github.com/creachadair/mds` | `v0.13.4` | todo |
| `github.com/davecgh/go-spew` | `v1.1.2-0.20180830191138-d8f796af33cc` | todo |
| `github.com/distribution/reference` | `v0.6.0` | todo |
| `github.com/djherbis/fscache` | `v0.10.1` | todo |
| `github.com/docker/go-connections` | `v0.6.0` | todo |
| `github.com/docker/go-units` | `v0.5.0` | todo |
| `github.com/ebitengine/purego` | `v0.10.0` | todo |
| `github.com/envoyproxy/go-control-plane/envoy` | `v1.36.0` | todo |
| `github.com/envoyproxy/protoc-gen-validate` | `v1.3.0` | todo |
| `github.com/felixge/httpsnoop` | `v1.0.4` | todo |
| `github.com/go-errors/errors` | `v1.5.1` | todo |
| `github.com/go-jose/go-jose/v4` | `v4.1.4` | todo |
| `github.com/go-logr/logr` | `v1.4.3` | todo |
| `github.com/go-logr/stdr` | `v1.2.2` | todo |
| `github.com/go-ole/go-ole` | `v1.2.6` | todo |
| `github.com/goccy/go-json` | `v0.10.2` | todo |
| `github.com/google/flatbuffers` | `v23.5.26+incompatible` | todo |
| `github.com/google/s2a-go` | `v0.1.9` | todo |
| `github.com/googleapis/enterprise-certificate-proxy` | `v0.3.15` | todo |
| `github.com/googleapis/gax-go/v2` | `v2.22.0` | todo |
| `github.com/hashicorp/golang-lru` | `v1.0.2` | todo |
| `github.com/jmespath/go-jmespath` | `v0.4.0` | todo |
| `github.com/klauspost/compress` | `v1.18.5` | todo |
| `github.com/klauspost/cpuid/v2` | `v2.2.10` | todo |
| `github.com/kylelemons/godebug` | `v1.1.0` | todo |
| `github.com/lufia/plan9stats` | `v0.0.0-20211012122336-39d0f177ccd0` | todo |
| `github.com/magiconair/properties` | `v1.8.10` | todo |
| `github.com/moby/docker-image-spec` | `v1.3.1` | todo |
| `github.com/moby/go-archive` | `v0.2.0` | todo |
| `github.com/moby/moby/api` | `v1.54.1` | todo |
| `github.com/moby/moby/client` | `v0.4.0` | todo |
| `github.com/moby/patternmatcher` | `v0.6.1` | todo |
| `github.com/moby/sys/sequential` | `v0.6.0` | todo |
| `github.com/moby/sys/user` | `v0.4.0` | todo |
| `github.com/moby/sys/userns` | `v0.1.0` | todo |
| `github.com/moby/term` | `v0.5.2` | todo |
| `github.com/munnerz/goautoneg` | `v0.0.0-20191010083416-a7dc8b61c822` | todo |
| `github.com/opencontainers/go-digest` | `v1.0.0` | todo |
| `github.com/opencontainers/image-spec` | `v1.1.1` | todo |
| `github.com/pelletier/go-toml` | `v1.9.5` | todo |
| `github.com/pierrec/lz4/v4` | `v4.1.18` | todo |
| `github.com/pkg/errors` | `v0.9.1` | todo |
| `github.com/planetscale/vtprotobuf` | `v0.6.1-0.20240319094008-0393e58bdf10` | todo |
| `github.com/pmezard/go-difflib` | `v1.0.1-0.20181226105442-5d4384ee4fb2` | todo |
| `github.com/power-devops/perfstat` | `v0.0.0-20240221224432-82ca36839d55` | todo |
| `github.com/prometheus/common` | `v0.66.1` | todo |
| `github.com/prometheus/procfs` | `v0.16.1` | todo |
| `github.com/segmentio/go-loggly` | `v0.5.1-0.20171222203950-eb91657e62b2` | todo |
| `github.com/shirou/gopsutil/v4` | `v4.26.3` | todo |
| `github.com/sirupsen/logrus` | `v1.9.4` | todo |
| `github.com/spiffe/go-spiffe/v2` | `v2.6.0` | todo |
| `github.com/stellar/go-xdr` | `v0.0.0-20260312225820-cc2b0611aabf` | todo |
| `github.com/stretchr/objx` | `v0.5.3` | todo |
| `github.com/stretchr/testify` | `v1.11.1` | todo |
| `github.com/tklauser/go-sysconf` | `v0.3.16` | todo |
| `github.com/tklauser/numcpus` | `v0.11.0` | todo |
| `github.com/yuin/gopher-lua` | `v1.1.1` | todo |
| `github.com/yusufpapurcu/wmi` | `v1.2.4` | todo |
| `github.com/zeebo/xxh3` | `v1.1.0` | todo |
| `go.opentelemetry.io/auto/sdk` | `v1.2.1` | todo |
| `go.opentelemetry.io/contrib/detectors/gcp` | `v1.39.0` | todo |
| `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc` | `v0.67.0` | todo |
| `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` | `v0.67.0` | todo |
| `go.opentelemetry.io/otel` | `v1.43.0` | todo |
| `go.opentelemetry.io/otel/metric` | `v1.43.0` | todo |
| `go.opentelemetry.io/otel/sdk` | `v1.43.0` | todo |
| `go.opentelemetry.io/otel/sdk/metric` | `v1.43.0` | todo |
| `go.opentelemetry.io/otel/trace` | `v1.43.0` | todo |
| `go.uber.org/atomic` | `v1.11.0` | todo |
| `go.yaml.in/yaml/v2` | `v2.4.2` | todo |
| `golang.org/x/crypto` | `v0.50.0` | todo |
| `golang.org/x/exp` | `v0.0.0-20240719175910-8a7402abbf56` | todo |
| `golang.org/x/mod` | `v0.34.0` | todo |
| `golang.org/x/net` | `v0.53.0` | todo |
| `golang.org/x/oauth2` | `v0.36.0` | todo |
| `golang.org/x/sys` | `v0.43.0` | todo |
| `golang.org/x/telemetry` | `v0.0.0-20260311193753-579e4da9a98c` | todo |
| `golang.org/x/text` | `v0.36.0` | todo |
| `golang.org/x/time` | `v0.15.0` | todo |
| `golang.org/x/tools` | `v0.43.0` | todo |
| `golang.org/x/xerrors` | `v0.0.0-20240903120638-7835f813f4da` | todo |
| `google.golang.org/genproto` | `v0.0.0-20260319201613-d00831a3d3e7` | todo |
| `google.golang.org/genproto/googleapis/api` | `v0.0.0-20260401024825-9d38bb4040a9` | todo |
| `google.golang.org/genproto/googleapis/rpc` | `v0.0.0-20260427160629-7cedc36a6bc4` | todo |
| `google.golang.org/grpc` | `v1.80.0` | todo |
| `google.golang.org/protobuf` | `v1.36.11` | todo |
| `gopkg.in/djherbis/atime.v1` | `v1.0.0` | todo |
| `gopkg.in/djherbis/stream.v1` | `v1.3.1` | todo |

## pnpm lockfile dependencies (web/explorer, web/dashboard, web/status)


### web/explorer

| `@tanstack/react-query` | `^5.62.0` |
| `@resvg/resvg-js` | `^2.6.2` |
| `clsx` | `^2.1.1` |
| `date-fns` | `^4.1.0` |
| `lightweight-charts` | `^4.2.2` |
| `lucide-react` | `^0.460.0` |
| `next` | `15.0.4` |
| `react` | `19.0.0` |
| `react-dom` | `19.0.0` |
| `satori` | `^0.12.0` |
| `sonner` | `^1.7.0` |
| `tailwind-merge` | `^2.5.4` |
| `zod` | `^3.23.8` |
| `@types/node` | `^22.9.3` |
| `@types/react` | `^19.0.0` |
| `@types/react-dom` | `^19.0.0` |
| `autoprefixer` | `^10.4.20` |
| `eslint` | `^9.15.0` |
| `eslint-config-next` | `15.0.4` |
| `openapi-typescript` | `^7.4.4` |
| `postcss` | `^8.5.0` |
| `prettier` | `^3.3.3` |
| `prettier-plugin-tailwindcss` | `^0.6.9` |
| `tailwindcss` | `^3.4.15` |
| `typescript` | `^5.6.3` |

### web/dashboard

| `clsx` | `^2.1.1` |
| `lucide-react` | `^0.460.0` |
| `next` | `15.0.4` |
| `react` | `19.0.0` |
| `react-dom` | `19.0.0` |
| `sonner` | `^1.7.0` |
| `tailwind-merge` | `^2.5.4` |
| `zod` | `^3.23.8` |
| `@types/node` | `^22.9.3` |
| `@types/react` | `^19.0.0` |
| `@types/react-dom` | `^19.0.0` |
| `autoprefixer` | `^10.4.20` |
| `eslint` | `^9.15.0` |
| `eslint-config-next` | `15.0.4` |
| `postcss` | `^8.5.0` |
| `prettier` | `^3.3.3` |
| `prettier-plugin-tailwindcss` | `^0.6.9` |
| `tailwindcss` | `^3.4.15` |
| `typescript` | `^5.6.3` |

### web/status

| `clsx` | `^2.1.1` |
| `lucide-react` | `^0.460.0` |
| `next` | `15.0.4` |
| `react` | `19.0.0` |
| `react-dom` | `19.0.0` |
| `tailwind-merge` | `^2.5.4` |
| `@types/node` | `^22.9.3` |
| `@types/react` | `^19.0.0` |
| `@types/react-dom` | `^19.0.0` |
| `autoprefixer` | `^10.4.20` |
| `eslint` | `^9.15.0` |
| `eslint-config-next` | `15.0.4` |
| `postcss` | `^8.5.0` |
| `prettier` | `^3.3.3` |
| `prettier-plugin-tailwindcss` | `^0.6.9` |
| `tailwindcss` | `^3.4.15` |
| `typescript` | `^5.6.3` |

## VERSIONS.md pinned upstream SHAs

See `VERSIONS.md` for hand-pinned upstream commit hashes audited per W04.
