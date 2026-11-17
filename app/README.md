# gatekeeper

gatekeeper is a small HTTP service that answers one question for a deploy
pipeline: may this team deploy this container image?

Each team is a **tenant** with its own **policy**. A policy can limit the
registries images come from, block tags such as `latest`, demand that images
are pinned by digest, and demand a signed **attestation** from a trusted build
system. gatekeeper keeps an audit trail of every decision, and it serves the
build logs that builders upload next to their images.

It uses only the Go standard library.

## Run it

```sh
go build -o gatekeeper ./cmd/gatekeeper
./gatekeeper -config config.example.json
```

Run the tests with `go test ./...`.

### Flags

| Flag | Meaning |
| --- | --- |
| `-config` | Path to the configuration file. Default `config.json`. |
| `-listen` | Address to listen on. Overrides the configuration file. |
| `-registry` | Registry assumed for images that do not name one. Overrides the configuration file. |
| `-debug` | Enable the debug endpoints. Overrides the configuration file. |

## Configuration

The configuration file is JSON. Unknown keys are rejected. See
[`config.example.json`](config.example.json) for a complete file.

| Key | Meaning |
| --- | --- |
| `listen` | Address the server binds to. Default `127.0.0.1:8080`. |
| `registry` | Registry assumed for image references that do not name one. Default `cgr.dev`. |
| `log_dir` | Directory holding build logs (`*.log`). |
| `archive_dir` | Directory holding log archives (`*.tar.gz`). |
| `audit_file` | Optional. File that receives every decision as a line of JSON. |
| `admin_token` | Token that authenticates callers of the admin API. |
| `cache_ttl_seconds` | How long a decision is reused. `0` turns the cache off. Default `60`. |
| `attestation_max_age_hours` | How old an attestation may be. Default `24`. |
| `rate_limit_per_minute` | Requests each tenant may make per minute. `0` turns the limit off. |
| `debug` | Enable the debug endpoints. |
| `builders` | Build systems whose attestations are verified. Each has a `name` and a `key`. |
| `tenants` | Teams that may ask for decisions. Each has a `name`, a `token` and a `policy`. |

For example, with `"registry": "cgr.dev"` the reference `chainguard/nginx:1.27`
is read as `cgr.dev/chainguard/nginx:1.27`.

### Policy

| Key | Meaning |
| --- | --- |
| `allowed_registries` | Registries images may come from. An entry also covers the same host on an explicit port, so `cgr.dev` admits `cgr.dev:443`. |
| `blocked_tags` | Tags that may not be deployed. |
| `require_digest` | The image must be pinned by digest (`name@sha256:...`). |
| `require_attestation` | The request must carry a valid attestation from a trusted builder. |
| `trusted_builders` | Builders whose attestations this tenant accepts. |

## API

Every `/v1` endpoint needs the calling tenant's token:
`Authorization: Bearer <tenant token>`. Every `/admin` endpoint needs the
admin token in the same header. Errors are returned as `{"error": "..."}`.

### `POST /v1/decide`

Asks whether the calling tenant may deploy an image.

```json
{
  "image": "cgr.dev/chainguard/nginx@sha256:...",
  "attestation": {
    "builder": "ci-main",
    "digest": "sha256:...",
    "issued_at": 1767225600,
    "signature": "..."
  }
}
```

`attestation` is optional. `tenant` may also be sent; older clients include
it. The answer:

```json
{"allow": true, "reason": "ok", "image": "cgr.dev/chainguard/nginx@sha256:...", "cached": false}
```

`image` is the reference in canonical form. A denied request is still a `200`
with `"allow": false` and the reason.

### `GET /v1/audit`

Returns the calling tenant's most recent decisions, newest first:
`{"entries": [...]}`. `limit` sets how many, from 1 to 500. Default 50.

### `GET /v1/logs`

Without parameters, lists the build logs: `{"logs": ["build-1.log", ...]}`.
With `name`, returns that log as plain text.

### `GET /v1/archives?name=<archive>.tar.gz`

Lists the files inside a log archive: `{"entries": [...]}`.

### Admin API

| Endpoint | Meaning |
| --- | --- |
| `GET /admin/policies` | Every tenant's policy: `{"policies": {...}}`. |
| `GET /admin/policies/{tenant}` | One tenant's policy. |
| `PUT /admin/policies/{tenant}` | Replace one tenant's policy. The body is a policy object. Returns `204`. |
| `POST /admin/cache/flush` | Drop every cached decision. Returns `204`. |

### Other endpoints

| Endpoint | Meaning |
| --- | --- |
| `GET /healthz` | Liveness check. Needs no token. |
| `GET /debug/config` | The settings the server is running with. Only available in debug mode. |

## Attestations

An attestation says that a builder produced the image with a given digest at a
given time. The builder signs it with the key it shares with gatekeeper:

1. Join the builder name, the image digest and `issued_at` (Unix seconds) with
   newlines: `ci-main\nsha256:...\n1767225600`.
2. Compute HMAC-SHA256 over those bytes with the builder's key.
3. Send the result hex-encoded as `signature`.

gatekeeper rejects attestations from unknown builders, with a bad signature,
older than `attestation_max_age_hours`, or issued more than two minutes in the
future.

## Layout

| Path | Holds |
| --- | --- |
| `cmd/gatekeeper` | The command: flags, configuration, server start and shutdown. |
| `internal/config` | Configuration loading and validation. |
| `internal/ref` | Image reference parsing. |
| `internal/policy` | Policies and their evaluation. |
| `internal/attest` | Attestation signing and verification. |
| `internal/cache` | The decision cache. |
| `internal/audit` | The audit trail. |
| `internal/logs` | Build logs and log archives. |
| `internal/ratelimit` | Per-tenant rate limiting. |
| `internal/server` | HTTP routes, authentication and handlers. |
