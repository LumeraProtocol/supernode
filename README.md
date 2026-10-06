# supernode

Lumera SuperNode: the service that Lumera validators run to provide Cascade storage and related services.

This repository publishes releases only. Development happens in a private repository.

- **Download:** [Releases](https://github.com/LumeraProtocol/supernode/releases). `sn-manager` reads them to update nodes automatically.
- **New releases** carry `SHA256SUMS` (verify with `sha256sum -c SHA256SUMS`) and a `supernode-<version>-src.tar.gz` source tarball.
- **Copied releases** (v2.5.2, and v2.5.3-testnet through v2.6.9-testnet) were copied here on 2026-10-06 from the previous repository and carry their original binaries only.
- **Source code archives:** GitHub's "Source code" archives on a release contain only this README.
- **Go:** existing versions of `github.com/LumeraProtocol/supernode/v2` stay available through the Go module proxy. New versions will be published as `go.lumera.io/supernode/v2`.

Issues are welcome; pull requests are not accepted.
