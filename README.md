# Muse Codex Adapter

A localhost-only Responses adapter for the Muse Spark 1.3 Contributor route in
CLIProxyAPI. It expands local JSON Schema references in tool parameters and
replaces only recursive back-edges with `{}` before forwarding the request.
All other Responses fields, upstream status codes, response bodies, and SSE
events are passed through.

## Boundaries

- Listens only on `127.0.0.1:18317`.
- Accepts `GET /healthz` and `POST /v1/responses` for
  `muse-spark-1.3-contributor` only.
- Forwards to the fixed OpenCode Go Responses endpoint; it does not store or
  log request bodies or credentials.
- Receives the selected upstream authorization and session headers from
  CLIProxyAPI. Credentials remain in CLIProxyAPI's local configuration.

## Build and verify

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -o "$HOME/.local/bin/muse-codex-adapter" .
```

The installed LaunchAgent is `com.hrygo.muse-codex-adapter`; its plist is kept
outside this source repository under `~/Library/LaunchAgents`.
