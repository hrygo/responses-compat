# Changelog

Notable user-facing changes are documented here. Versions are derived from Git tags.

## [Unreleased]

### Added
- Versioned strict configuration for fixed-upstream instances, exact model allowlists, compatibility profiles, independent transform overrides, and safe header-name allowlists.
- A byte-preserving `passthrough` profile for isolating upstream behavior before enabling compatibility transformations.
- Credential-free Muse and Muse-passthrough configuration examples, plus an operator deployment/rollback checklist.

### Changed
- Project/module and command identity is now Responses Compat (`responses-compat`); the no-config Muse defaults remain for upgrade compatibility.
- Successful responses with tool-name aliases fail closed when the media type is not a supported JSON or SSE type.
- Locally generated SSE rewrite-failure responses use the Responses Compat identity and provider-neutral wording.

## [0.1.0] - 2026-09-24

### Added
- Localhost-only Responses adapter for the Muse Spark 1.3 Contributor route, including recursive JSON Schema normalization.
- Deterministic aliases for function tool names exceeding the upstream 64-character limit, restored in JSON and SSE responses.
- Bounded response rewriting that fails closed on invalid or oversized JSON/SSE events.

### Changed
- Reasoning items omit unstable provider-scoped IDs to support stateless tool-turn replay.
- SSE headers are flushed before the first event to preserve streaming behavior.
