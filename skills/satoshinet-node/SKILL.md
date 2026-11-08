---
name: satoshinet-node
description: Deploy, upgrade, or build a SatoshiNet node on Linux or macOS. Ordinary nodes synchronize and support BTC lucky mining; miners add asset staking and SatoshiNet PoS mining; core nodes add private-channel access and channel-contract services. Prefer official GitHub releases or build the selected target from source.
---

# Deploy or Build a SatoshiNet Node

Use one workflow for three cumulative node roles: an ordinary node provides synchronization, validation, and BTC lucky mining; a miner includes all ordinary-node capabilities and adds asset staking and SatoshiNet PoS mining; a core includes all miner capabilities and adds private-channel access and channel-contract services. This skill does not deploy a bootstrap node. All instructions are public and operator-independent; no maintainer host, wallet, or development workspace is required.

## Select the target

Choose the role from the operator's intent, installation method (`release` by default, or explicitly requested `source`), and whether BTC lucky mining is enabled. These are workflow choices, not additional node command-line flags. For a build-only request, collect only the target, OS/architecture, source revisions, and output directory, follow [Source builds](references/build.md), then stop at verified artifacts. The wallet, network, and service inputs below apply to deployment.

| Target | Purpose | Current build output | Go build tag | `generate` | Signing wallet |
| --- | --- | --- | --- | --- | --- |
| `ordinary` | Synchronization, validation, and BTC lucky mining | `satoshinet` | None | `false` | Not needed |
| `miner` | All ordinary-node capabilities, plus asset staking and SatoshiNet PoS mining | `satoshinet_miner` + `wallet.so` | `wallet_plugin` | `true` | Required |
| `core` | All miner capabilities, plus private-channel access and channel-contract services | `satoshinet_core` + `stpd.so` | `stp_plugin` | `true` | Required |

All three targets support BTC lucky mining. Its runtime switch is independent of the PoS `generate` flag; apply the operator's choice and the defaults in [Configuration](references/configuration.md). The operator can disable BTC mining for a synchronization-only node. Archive names may differ from the source output names above; use the selected release's packaging.

## Collect inputs and check prerequisites

- Target Linux/macOS host, authorized connection method, deployment account, absolute application/data directories, and new installation or upgrade. Detect OS, architecture, resources, existing node instances, and occupied ports.
- Release repository/version or compatible source revisions. Default public repository: [sat20-labs/satoshinet](https://github.com/sat20-labs/satoshinet).
- Operator-supplied valid 33-byte compressed public key for BTC mining or a signing role. A `miner`/`core` key must match its actual unlocked wallet and staking identity. Never substitute a maintainer key or reward address.
- Current mainnet P2P peers, bootstrap identity/STP endpoint, selected L1 indexer, and any upstream core identity/STP endpoint, taken from the release's published network configuration or an operator-verified provider. P2P and HTTP endpoints are different services.

Public service examples are L1 `https://apiprd.ordx.market/btc/mainnet`, L2 queries `https://apiprd.ordx.market/satsnet/mainnet`, and core STP `https://apiprd.ordx.market/stp/mainnet`. They are replaceable services, not required deployment hosts. Verify reachability from the target, mainnet identity, and current provider keys. External hosts need public endpoints rather than inaccessible LAN examples.

Apply these role-specific identity rules before startup:

| Target | `serverpubkey` | Current L1 indexer identity requirement |
| --- | --- | --- |
| `ordinary` with BTC mining | Selected service/co-signing key | Verify it belongs to the chosen mining provider |
| `miner` | Upstream core key, also used by the wallet's `s@KEY@URL` peer | Indexer key equals the local wallet or upstream core key |
| `core` | Empty, selecting the core startup branch | Indexer key equals the local wallet or bootstrap key |

For `miner`/`core`, verify admission and confirmed stake through a healthy synchronized L2 peer before relying on the new local indexer. Use the chosen release's staking requirements and supported wallet interface; source reviewers can inspect `GetStakeAssetName`/`GetStakeAssetAmt` in `indexer/common` at the relevant L1 height. Do not hardcode old asset amounts. Check existing/pending stake before proposing another transaction. New staking or channel funding requires wallet-side operator confirmation of chain, asset, amount, destination, and fees. Ordinary-miner SDK staking uses `StakeToBeMinner(false, feeRate)`; `true` selects core staking.

## Acquire the executable and optional plugin

1. Prefer [official GitHub releases](https://github.com/sat20-labs/satoshinet/releases), or an official repository explicitly supplied by the operator. Resolve the requested tag or latest stable release to an exact version; read its notes and actual asset metadata.
2. Select assets for the chosen **target, OS, and architecture**: Linux or macOS (`darwin`), and the actual `amd64`/`arm64` architecture. Use published asset URLs; do not invent file names or assume every combination is available.
3. Follow release OS/runtime requirements and verify published checksums/digests when available. Record installed hashes; if only a local hash is available, state that verification limit. Binary deployment needs no Go installation or source checkout.
4. Keep the executable and required plugin from the same release/platform/build. Preserve the package's companion-file layout; macOS Go plugins may still be named `.so`. Do not mix core and wallet plugins.
5. If a matching asset is missing, report the gap. Offer source compilation if desired; do not silently substitute an internal binary, a prerelease, or unrelated revisions. For source installation or build-only work, read [Source builds](references/build.md) and build only the selected target.

Stage artifacts outside the live data directory. For upgrades, back up files being replaced, stop only the confirmed target process/service with SIGINT, and wait for exit before replacing an executable/plugin pair. Back up databases while stopped and preserve that node's wallet/account selection, channels, contracts, stake history, and chain state. Do not replace its identity with another operator's database.

## Prepare configuration and signing prerequisites

Read [Configuration](references/configuration.md) for the shared node config and the selected role's plugin differences. Replace all placeholders, use explicit absolute paths, and generate local RPC credentials on the target without displaying them. Restrict secret-bearing configuration, normally to mode 0600. The L1 settings in both configs must identify the same service.

For `miner`/`core`, the operator creates/imports the wallet and prepares its unlock credential through a controlled local or wallet interface. Confirm unattended unlock, usable account selection, and the expected public key before long-term service startup. A flag or wallet balance is not evidence of mining eligibility.

The agent must **never read `DATA_DIR/w.id`, other password files, mnemonics, or private keys**, including via broad searches/archive commands. The node program may read its configured credential under the operator's authorization. Avoid capturing initialization output containing a mnemonic. If logs expose a secret, notify the operator without reproducing it. An `ordinary` node needs no wallet initialization or credential copying.

## Configure startup at boot: required

Run the installed executable with absolute `--homedir DATA_DIR --configfile CONFIG_PATH --txindex` arguments and `APP_DIR` as its working directory. Reuse an existing manager if it provides unattended boot startup with the correct account, paths, persistent configuration, and restart policy.

- **Linux/systemd:** use the intended service account, absolute paths, `Wants`/`After=network-online.target`, `Nice=0`, `KillSignal=SIGINT`, `TimeoutStopSec=120`, `Restart=on-failure`, `RestartSec=10`, and `WantedBy=multi-user.target`. Validate the unit, run `systemctl daemon-reload` and `systemctl enable --now SERVICE`, then confirm `enabled` and `active`.
- **macOS/launchd:** use a system-domain LaunchDaemon with a unique label, absolute `ProgramArguments`, `WorkingDirectory`, intended `UserName`, writable stdout/stderr paths, `RunAtLoad=true`, `KeepAlive` with `SuccessfulExit=false`, `ThrottleInterval=10`, and `Nice=0`. Validate with `plutil -lint`, install under `/Library/LaunchDaemons` with required ownership/permissions, bootstrap/enable it, and verify `launchctl print system/LABEL`. A LaunchAgent starts at login and does not satisfy unattended boot startup.

Do not reboot unless requested. Report boot configuration verification separately from an actual reboot test. For signing roles, startup must not wait for interactive password input.

## Verify the common path, then the selected role

1. Record target, installation method, release/tag or source commits, exact asset URLs/hashes, platform, directories, public identities, and service startup status.
2. Query authenticated local RPC `getmininginfo`: mainnet and the target's expected `generate` value. Compare local `/mainnet/bestheight` and a common-height block hash with a healthy peer, and check `/mainnet/health`.
3. **`ordinary`:** confirm no signing-wallet or PoS startup is required. If BTC mining is enabled, perform the BTC checks below.
4. **`miner`:** require successful L2 `/mainnet/v3/miner/check/PUBKEY` with `data=true`, inspect `info`, and find wallet readiness, `StartWalletMgr completed`, and `Start pos miner.` in complete startup logs. Confirm that no public channel service was added for this instance.
5. **`core`:** require successful miner and core eligibility checks (`/mainnet/v3/miner/check/PUBKEY`, `/mainnet/v3/corenode/check/PUBKEY`) with `data=true`, signing-wallet readiness, `StartSTP completed`, and `Start pos miner.`. Confirm local STP `/health` on the configured public listener returns `ready=true`, and query node information through the actual public STP endpoint to verify reachability and identity. Verify private-channel access and read-only channel-contract queries through the supported protocol API; inspect existing channels' current state/balances and existing contracts' current state where available. New funded channels or contract deployment require explicit task authorization. PoS/BTC activity alone does not establish these service capabilities.
   If a market or PWA queries this core across origins, also run the [browser-origin contract query](references/configuration.md) against the public URL.
6. For every target, run the shared BTC checks below when enabled. Observe several minutes in the current session for stable PID, restart count, CPU/memory, validator activity where applicable, and relevant errors. A miner not being selected to produce a block during a short interval is not itself failure. Do not promise a PoS or Bitcoin block within the observation period.

Search the **complete startup logs** because startup records can scroll out of a short tail. Current signing builds load and unlock the plugin, wait for the chain and local indexer to catch up, start the wallet or STP runtime, check eligibility, then start PoS. For all roles, BTC lucky mining starts once the SatoshiNet chain and local indexer have caught up. Check the BTC job, hashrate, and CPU usage after the miner starts.

### BTC lucky mining checks

When enabled, default workers to all available logical CPUs (`nproc` on Linux, `sysctl -n hw.logicalcpu` on macOS), accounting for imposed CPU limits; use `btcluckymininglowpriority=false` and process nice 0 unless the operator overrides them. Retain the role's `generate` value.

Verify the configured L1 `/bestheight` and `/btc/lucky/info`: mainnet, `enabled`, `running`, `btcRpcConnected`, and no current backend error. Derive and record the expected reward address using the project's sorted 2-of-2 mainnet P2WSH construction: operator plus upstream service for `ordinary`/`miner`, operator plus bootstrap for `core`. Check existing `miningaddr` selection and compare the actual logged reward address.

Find `BTC lucky miner started with N jobs` and `start succeeded running=true` in full logs, confirming workers, provider, and normal priority. Backend `activeJobs` counts cached jobs, not this host's workers. Template height normally refers to the next Bitcoin block. Startup `hashrate=0` is an initial snapshot; ordinary RPC `hashespersec` is not the live BTC lucky hashrate.

Wait for SatoshiNet/indexer catch-up before attributing CPU usage to mining alone. Process CPU 100% represents one logical CPU; divide by available logical CPUs to express its share of the host. If historical rewards are requested, combine submission evidence with `/mainnet/v3/btc/lucky/blocks?pubkey=PUBLIC_KEY&start=0&limit=20`, distinguishing submission, BTC confirmation, and indexed SatoshiNet rewards.

## Diagnose and report

For password errors, the operator corrects the credential locally; never retrieve it. For unavailable wallet selection, diagnose restoration. For plugin incompatibility, install a matched release or rebuild the pair. For indexer key API 404/mismatch, check provider API/version/identity rather than altering KV permissions. For `not a miner, exit`, check stake and indexing instead of leaving an unexplained restart loop. Preserve evidence and propose minimal production code/state repairs before applying them; do not clear databases to force startup.

Report common deployment/runtime evidence and BTC mining status for every role, plus staking/PoS results for `miner` and `core`, and private-channel/channel-contract service results for `core`. Include boot persistence and unfinished checks. For a build-only task, report artifact/build verification and leave running nodes untouched.
