# Node Configuration by Target

Use the target and BTC-mining choice selected in [the skill](../SKILL.md). These are input templates, not directly runnable configurations: replace every placeholder before startup.

## Shared node configuration

Use absolute application, data, and configuration paths. The executable may be installed as `APP_DIR/satoshinet` regardless of its source output name. Add locally generated `rpcuser` and `rpcpass` without printing them; restrict configuration permissions, normally to 0600.

```ini
mainnet=1
synctoheight=0
generate=GENERATE
miningpubkey=OPERATOR_PUBLIC_KEY
serverpubkey=SERVER_PUBLIC_KEY
indexerscheme=L1_SCHEME
indexerhost=L1_HOST
indexerproxy=btc/mainnet
addpeer=P2P_HOST:9526
rpclisten=127.0.0.1:9527
notls=1
txindex=1
btcluckymining=BTC_LUCKY
btcluckyminingjobs=WORKER_COUNT
btcluckymininglowpriority=false
```

All targets include BTC lucky mining capability; `generate` controls the additional SatoshiNet PoS functionality. Apply the selected role:

- **`ordinary`:** `generate=false`. With BTC mining, use the operator's supplied public key and the selected service/co-signing key for `serverpubkey`. A synchronization-only node can omit mining identity fields. No signing plugin or `conf.yaml` is needed by the plain build.
- **`miner`:** retain ordinary-node capabilities, including BTC lucky mining, and add staking/PoS with `generate=true`, a public key matching the unlocked wallet/stake, and `serverpubkey=UPSTREAM_CORE_PUBLIC_KEY`. Install `wallet.so` beside the executable.
- **`core`:** retain all miner capabilities and add private-channel access and channel-contract services. Use `generate=true`, a public key matching the unlocked wallet/stake, and an **empty** `serverpubkey` (`serverpubkey=`). Install `stpd.so` in the application working directory. This selects the current core startup branch and its operator/bootstrap channel address.

`BTC_LUCKY` controls the shared BTC mining capability independently of the role: the existing deployment defaults are `true` for `ordinary` and `false` for `miner`/`core` unless requested. The operator can explicitly enable or disable it for any target. When enabled, `WORKER_COUNT` defaults to all available logical CPUs, with normal priority. `synctoheight=0` keeps the node running after synchronization.

Use the selected provider's L1 scheme/host/proxy; for example, `https`, `apiprd.ordx.market`, `btc/mainnet` form the public market URL. This is only valid when that provider meets the chosen role's identity requirements. Use published mainnet P2P peers, not STP HTTP URLs. If ports are occupied, adapt the instance's node/indexer configuration and all corresponding local endpoints consistently.

## Shared signing-plugin configuration

Only `miner` and `core` use this `APP_DIR/conf.yaml`. Both current plugins load it beside the executable, while core `stpd.so` loading also depends on the working directory. Set `WorkingDirectory=APP_DIR` for both operating systems.

```yaml
env: prd
chain: mainnet
mode: PLUGIN_MODE
peers:
  - b@BOOTSTRAP_PUBLIC_KEY@BOOTSTRAP_STP_URL
indexer_layer1:
  scheme: L1_SCHEME
  host: L1_HOST
  proxy: btc/mainnet
indexer_layer2:
  scheme: http
  host: 127.0.0.1:9528
  proxy: mainnet
log: info
wallet:
  mode: internal
  stake: false
  psfile: wallet.password
```

- **`miner`:** set `mode: client` and append `s@UPSTREAM_CORE_PUBLIC_KEY@UPSTREAM_CORE_STP_URL` to `peers`, matching `serverpubkey`. The wallet plugin starts no public STP channel listener; this instance needs no new channel-service listener on 9529/9530.
- **`core`:** set `mode: server` to provide private-channel access and channel-contract services, and add the following public protocol listener:

```yaml
rpc:
  scheme: http
  host: 0.0.0.0:9529
  proxy: mainnet
```

The core public listener is HTTP; preserve the chosen public endpoint/reverse proxy rather than adding mandatory node-side TLS. Its management service is separate, normally loopback port 9530 with existing access controls preserved. Node RPC/indexer normally use 9527/9528. Do not stop unrelated instances using other ports.

If a market or PWA on another origin calls this core directly from the browser, add the calling page's exact HTTPS origin to the top-level public RPC settings in `conf.yaml`. Use the iframe's origin if the embedded market sends the request. List each trusted caller separately (scheme, host, and port if non-default; no paths or wildcards). For example, replace the example origin before use:

```yaml
public_rpc:
  browser_allowed_origins:
    - https://market.example.org
```

After restart, verify through the same HTTPS endpoint used by the page. Set `PAGE_ORIGIN` to the calling origin and `STP_BASE` to the public STP URL including the network prefix:

```sh
curl -isS -H "Origin: ${PAGE_ORIGIN}" "${STP_BASE%/}/info/contracts/deployed"
```

Require HTTP 200, an `Access-Control-Allow-Origin` header matching `PAGE_ORIGIN`, and a successful contract response (`code: 0`; an empty list is valid). A health check without an Origin header does not verify browser access.

Keep identical L1 settings across the node and plugin configs. Keep an existing node's actual `psfile` name: the program resolves it relative to `DATA_DIR` for authorized unattended unlock, without agent inspection. Preserve usable wallet/account selection and verify its public identity. `wallet.stake=true` is not proof of eligibility; keep automatic staking disabled unless the operator has authorized that transaction. Verify confirmed stake through the indexer.
