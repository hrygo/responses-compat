# Changelog

Notable user-facing changes are documented here. Versions are derived from Git tags.

## [0.1.0] - 2026-09-24

### Added
- Localhost-only Responses adapter for the Muse Spark 1.3 Contributor route, including recursive JSON Schema normalization.
- Deterministic aliases for function tool names exceeding the upstream 64-character limit, restored in JSON and SSE responses.
- Bounded response rewriting that fails closed on invalid or oversized JSON/SSE events.

### Changed
- Reasoning items omit unstable provider-scoped IDs to support stateless tool-turn replay.
- SSE headers are flushed before the first event to preserve streaming behavior.
