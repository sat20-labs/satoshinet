# Build SatoshiNet from Source

Read this reference for a source installation or build-only task selected in [the node skill](../SKILL.md). Prefer official prebuilt releases for deployment. Build only the selected target; producing artifacts does not automatically replace a running node.

Target capabilities are cumulative: `ordinary` includes synchronization, validation, and BTC lucky mining; `miner` adds asset staking and SatoshiNet PoS mining; `core` adds private-channel access and channel-contract services. All three builds support BTC lucky mining, configured independently at runtime. Build tags select the additional signing/service implementation.

## Select source revisions and role

Collect the role (`ordinary`, `miner`, or `core`), target OS/architecture, output directory, and official release tag/commit or another operator-selected revision. Read that revision's build instructions and pin the source revisions for all required repositories. Do not assume that different repositories use the same tag, or combine unrelated default-branch heads for a release build.

The public SatoshiNet source is `https://github.com/sat20-labs/satoshinet`. Current builds use local module replacements; arrange repositories beneath an operator-chosen workspace:

```text
WORKSPACE/
  satoshinet/
  indexer/
  sat20wallet/sdk/     # miner and core dependencies
  transcend/          # core STP source, when access is available
```

Obtain dependency repository URLs and compatible commits from the selected release/build documentation and the modules' declared paths. Inspect **all local `replace` directives** in the selected dependency graph before downloading modules. Additional siblings, such as `xaplib/chainindex`, may be required by a chosen SDK revision. Resolve those actual requirements instead of assuming the diagram is exhaustive. Do not delete replacement directives or rewrite production module files to hide missing dependencies.

An ordinary build normally needs the node and its local indexer dependency; a miner additionally needs the wallet SDK; a core additionally needs the STP implementation. Core compilation requires authorized access to all STP sources for that revision. If those sources are unavailable, report that limitation and use a matching published core binary/plugin when available. Do not claim that a wallet-plugin build provides core channel services.

## Prepare a native build environment

- Support the actual Linux or macOS host and its CPU architecture. Prefer native builds, particularly for plugin pairs; changing `GOOS`/`GOARCH` alone does not establish cross-compilation support.
- Read `go` and `toolchain` directives and any release-specific compiler requirements. Use one matching Go toolchain for the executable, plugin, and shared dependencies. Check `go version` and `go env GOHOSTOS GOHOSTARCH GOOS GOARCH CGO_ENABLED GOROOT` rather than inheriting an unrelated `GOROOT`.
- Install the C toolchain required by that revision's dependencies/plugin build and use the selected release's CGO settings. On macOS this normally means an available Xcode command-line toolchain; Linux needs the appropriate native compiler/development libraries.
- Go plugins on Linux and macOS must match the executable's toolchain, compatible build flags/tags/environment, and shared dependency source. Keep the resulting pair together. A macOS Go plugin can still use the `.so` filename expected by the program.
- Build in a separate checkout/output directory, outside any live node data directory. No wallet, private key, password, or `data/w.id` is needed. Do not scan or copy runtime credentials as part of building.

## Build only the selected role

Use absolute paths in place of `WORKSPACE` and `OUT_DIR` below. Create `OUT_DIR` first and replace every placeholder. The following commands reflect the current repository's build entry points; reconcile them with the selected revision and its release-specific tags before executing. Preserve matching compilation settings across the pair and check each command's exit status.

### Ordinary node: no signing plugin

From `WORKSPACE/satoshinet`:

```sh
go build -ldflags='-s -w' -o OUT_DIR/satoshinet .
```

This corresponds to the plain build in `build.sh`. Run it with `generate=false` for the ordinary/BTC-only role. No `wallet.so` or `stpd.so` is required for this build.

### Ordinary staking miner: wallet plugin

From `WORKSPACE/sat20wallet/sdk/plugin`:

```sh
go build -buildmode=plugin -ldflags='-s -w' -o OUT_DIR/wallet.so main.go
```

From `WORKSPACE/satoshinet`:

```sh
go build -tags wallet_plugin -ldflags='-s -w' -o OUT_DIR/satoshinet_miner .
```

These are the artifact roles in `build_miner.sh`. Keep `satoshinet_miner` and `wallet.so` from this build together. The installed executable may be named `satoshinet` if deployment configuration uses that name.

### Core node: STP plugin

From `WORKSPACE/transcend/plugin`:

```sh
go build -buildmode=plugin -ldflags='-s -w' -o OUT_DIR/stpd.so main.go
```

From `WORKSPACE/satoshinet`:

```sh
go build -tags stp_plugin -ldflags='-s -w' -o OUT_DIR/satoshinet_core .
```

These correspond to `build_core.sh`. Check whether the selected revision requires additional release/mainnet tags; apply its documented settings consistently. Do not use test/fault-injection tags for a production package. Keep the core executable and `stpd.so` together.

The existing shell build scripts can be used after inspecting their behavior. Some end with an unconditional `build completed` message, so a final echo or script exit status alone is insufficient evidence that every build step succeeded. Check individual results and newly produced artifacts; do not reuse stale output from an earlier failed build.

## Verify and hand off

1. Confirm the expected artifact set, fresh output files, target format/architecture with `file`, and toolchain/module build information with `go version -m` where available.
2. Record source commit IDs, Go version, OS/architecture, build flags/tags, relevant CGO settings, and SHA-256 for every artifact (`sha256sum` on Linux or `shasum -a 256` on macOS).
3. Check the node's non-secret `--version` output. A plugin build succeeding does not by itself prove runtime loading; perform that check during authorized deployment with the matching executable, wallet, and config. Do not create wallets or access credentials merely to finish a build.
4. Report any dependency/access/build failures without silently removing features or changing module versions. This is a locally built artifact set, not an official GitHub release.
5. For a source installation, return to the shared deployment workflow in [the node skill](../SKILL.md). For build-only work, report the artifacts and stop. Preserve the old node until a verified replacement is ready.

Go's plugin compatibility requirements are documented in the [official plugin package documentation](https://pkg.go.dev/plugin). The selected release and its checked-out source remain the authority for supported platforms and exact build settings.
