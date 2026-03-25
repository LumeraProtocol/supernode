# System Tests (Cascade E2E)

This suite brings up a local Lumera chain and three local supernodes, then runs an end‑to‑end Cascade flow.

## Run

- Prepare supernodes (build binaries + configs + keys):
  - `make setup-supernodes`
- Execute only Cascade E2E:
  - `make test-cascade`
- Execute all system tests:
  - `make test-e2e`

The tests manage `INTEGRATION_TEST` internally so local loopback addresses (127.0.0.1/localhost) are accepted by the P2P layer during test runs.

By default, system tests also perform a hard pre/post runtime cleanup for stale `lumerad`/`supernode` listeners on known test ports to reduce flaky reruns.
Set `SYSTEM_TEST_HARD_CLEANUP=0` to disable this behavior.

## Layout

- `config.test-1.yml`, `config.test-2.yml`, `config.test-3.yml` — Supernode configs (hosts on 0.0.0.0, P2P ports 4445/4447/4449; gRPC 4444/4446/4448)
- `supernode-data*` — Per‑node runtime directories created by the setup script
- `supernode-utils.go` — Helpers to start/stop the supernode processes for tests

See `docs/TESTING.md` for deeper details and troubleshooting.
