# Muse Codex Adapter

A localhost-only Responses adapter for the Muse Spark 1.3 Contributor route in
CLIProxyAPI. It expands local JSON Schema references in tool parameters,
replaces only recursive back-edges with `{}`, and assigns deterministic aliases
to function names longer than the upstream 64-character limit. It restores
original function names in JSON and SSE responses so Codex can invoke its tools.
Other Responses fields and upstream non-2xx status/body pairs pass through unchanged; successful responses retain their upstream status when rewriting succeeds.

## Boundaries

- Listens only on `127.0.0.1:18317`.
- Accepts `GET /healthz` and `POST /v1/responses` for
  `muse-spark-1.3-contributor` only.
- Forwards to the fixed OpenCode Go Responses endpoint; it does not store or
  log request bodies or credentials.
- Receives the selected upstream authorization and session headers from
  CLIProxyAPI. Credentials remain in CLIProxyAPI's local configuration.
- When aliases require rewriting, JSON responses and individual SSE events are
  bounded at 64 MiB after expansion. Invalid or oversized JSON rewrites fail
  closed with HTTP 502; invalid or oversized SSE rewrites emit a terminal
  Responses response.failed event and close the stream instead of forwarding a
  leaked alias or reporting a successful [DONE] terminator.

## Build and verify

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -o "$HOME/.local/bin/muse-codex-adapter" .
```

The installed LaunchAgent is `com.hrygo.muse-codex-adapter`; its plist is kept
outside this source repository under `~/Library/LaunchAgents`.
