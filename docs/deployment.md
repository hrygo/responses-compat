# Deployment and rollback checklist

This guide is an operator checklist, not evidence that the current machine has been migrated. The current task only builds and tests an offline candidate. Do not stop or replace a running service, run real upstream requests, create a release tag, or publish artifacts without the corresponding explicit authorization.

## Before a service change

1. Confirm the target machine, service owner, intended listener, maintenance window, and rollback operator.
2. Inspect the actual loaded service and plist instead of inferring paths from a Git tag or this repository directory. On macOS, the expected historical labels are `com.hrygo.muse-codex-adapter` and `com.hrygo.responses-compat`; verify they are still accurate before use.
3. Read the loaded plist `ProgramArguments` and `EnvironmentVariables` to identify the executable and configuration path. Do not copy credentials into this repository, command arguments, logs, or a report.
4. Run the full offline checks and build to a staging path outside the source tree:

   ```sh
   STAGING_DIR=$(mktemp -d)
   go test ./... -count=1
   go test -race ./... -count=1
   go vet ./...
   go build -trimpath -o "$STAGING_DIR/responses-compat" .
   shasum -a 256 "$STAGING_DIR/responses-compat"
   ```

5. Preserve rollback material before stopping anything. Copy the exact currently loaded binary and plist to a restricted backup directory, record their SHA-256 values, and confirm both copies are readable. Do not treat a Git tag or a previous build as a backup of the running binary. Keep the current CLIProxyAPI configuration and credentials unchanged.
6. Validate the replacement plist with `plutil -lint` and verify it points to the staged/released binary, the intended configuration file, and the intended listener. Do not let old and new processes compete for `127.0.0.1:18317`.

## Switch and verify

Proceed only after the service-change authorization gate is satisfied.

1. If the replacement is configured on an isolated port (for example `127.0.0.1:18318`), start it only for local health validation and verify `GET /healthz` returns `204`. Health status does not prove upstream compatibility.
2. For a same-port replacement, unload the old LaunchAgent using the exact verified plist, confirm its process has stopped, then load the new plist. Use the actual paths discovered above; do not copy a command from shell history or assume this repository is the installed source.
3. Verify the new process executable and arguments from the operating system, then check the local health endpoint:

   ```sh
   curl -i http://127.0.0.1:18317/healthz
   ```

4. Only with separate real-request authorization, run a minimal synthetic end-to-end tool round trip and SSE check through the intended client and CLIProxyAPI route. Avoid side-effecting tools and do not place authorization values on a command line. Record the tested client/service versions, configuration profile, failing boundary, and result. An HTTP 204 alone is not an end-to-end acceptance test.
5. Preserve upstream non-2xx behavior and confirm the client can receive the original tool name back before declaring the service usable.

## Rollback

If startup, health, request, or SSE verification fails:

1. Unload the new LaunchAgent using its verified plist and confirm the new process has stopped.
2. Restore the backed-up old binary and plist to their exact prior paths, preserving permissions. Do not overwrite unrelated files; if a target path has changed since backup, stop and inspect before restoring.
3. Reload the old plist and verify the process points to the restored binary. Check `GET /healthz`, then confirm the existing CLIProxyAPI route is restored.
4. Record the failure boundary and retain the new candidate for offline diagnosis. Do not repeatedly retry real requests or move/delete Git history to roll back a runtime deployment.

## Version and publication gate

- A successful local build or health check is not a published release.
- Keep the existing `v0.1.0` history unchanged. Do not move that tag.
- The planned `v0.2.0` candidate requires the authorized layered compatibility checks in the implementation plan, including real upstream/client comparisons. Do not tag or publish while a required compatibility case remains unresolved or unverified.
- Report source commit, build hash, runtime executable/hash, actual test evidence, unverified cases, release state, and rollback location separately.
