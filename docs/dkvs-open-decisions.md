# DKVS Open Decisions

更新时间：2026-07-04

本文只列出当前 DKVS 需求文档中不能在 `satoshinet/indexer` 内部安全臆造的剩余决策。现有代码已经保守实现了接口、默认关闭策略、测试 stub 和本地验证能力；下列问题确定后，才能继续接入真实生产实现。

## 1. Ordinals DID Resolver

当前阻塞：

- `/name/<name>` 已支持 L1 indexer `/ns/name/:name` owner address + record pubkey p2tr address 校验。
- `/svc/<service_name>/...` 可复用同一 resolver 形态，但生产 service name 映射还需要确认。
- 现有 SatoshiNet referrer 数据只有 `Name` 和 `BindBlock`，不足以判断 owner、active、DKVS signing key 或 key rotation。
- 如果 name sat 已 ascend 到 SatoshiNet，L2 owner 是否优先于 L1 `/ns/name/:name` 仍需确认。

需要决策：

1. 已 ascend 到 SatoshiNet 的 name sat 是否覆盖 L1 `/ns/name/:name` owner。
2. `name_id` 如何生成和持久映射：继续使用 `NormalizeNameID(canonical_name)`，还是由 DID 协议显式给出。
3. 除 owner p2tr address 外，是否还需要 DID text/record 字段声明 service-specific signing key 或多 key 列表。
4. L1 NS active / revoked / expired 状态是否只以 `/ns/name/:name` 返回 200 且 `data.address` 非空为准。
5. owner/key rotation 后历史 DKVS record 的规则：当前实现是同 key 同 pubkey 更新不再 resolve；如果 indexer 本地调用 `NotifyDKVSNameTransfers(names)` 标记 name 已转移，则下一次写入必须 resolve；pubkey 变更时也会 resolve 当前 owner，新 owner 即使低 seq 也可替换。
6. service name 是否完全来自 DID，还是允许系统预置 service name；如果允许，需要系统预置列表和签名 key 来源。

推荐的最小生产接入：

- 使用当前 L1 `indexer` 的 `GET /ns/name/:name`，返回 `data.address` 作为 owner address。
- SatoshiNet DKVS 通过 `L1NSResolver` 调用该 API，并从 record pubkey 派生 p2tr address 对比 owner address。
- resolver unavailable 时继续拒绝 `/name`、`/svc` 写入，保持主网安全。

代码接入点：

- `indexer/indexer/dkvs.DIDResolver`
- `indexer/indexer/dkvs.L1NSResolver`
- `indexer/indexer/dkvs.HTTPDIDResolver`
- `dkvs.Config.Resolver`
- `indexer.Config.DKVS.Resolver`
- `indexer.Config.DKVS.ResolverL1NSBaseURL`
- `IndexerMgr.SetDKVSResolver`
- 当前 static resolver 只用于测试，不作为主网默认。

## 2. DKVS Pool Fee Verifier

当前阻塞：

- `JSONFeeVerifier` 只能验证 fee proof 结构、字段一致性和可选 payer signature。
- AUTOPAY template contract verifier 已可通过 `getcontractstate` 校验 `autopay.tc` active、deployer/payer、recipient、fee asset、expiry 和按 full-size record 换算容量。
- 测试网已有硬编码默认 AUTOPAY 参数；主网仍未定义默认 contract/deployer/recipient/fee asset/full-record fee，也未定义 miner 收益分配。

需要决策：

1. 主网 AUTOPAY 合约默认参数：deployer、recipient、fee asset、full-record fee、默认 nonce/地址和是否要求 proof signature。
2. miner 收益分配规则：按区块 miner、按在线 miner 权重、按 core/miner 类型权重，还是由 AUTOPAY/Pool 合约单独结算。
3. AUTOPAY 是否就是第一版唯一生产 fee proof，还是仍需要 ONESHOT/LEASE。
4. 如果需要 ONESHOT：payment proof 格式、确认数、可覆盖 namespace/size/expiry 和防重放规则。
5. 如果需要 LEASE：plan_id、payer、scope、namespace、prefix、quota、expiry、daily mailbox quota。
6. `record_size` 计费口径：当前实现使用 `RecordSize(record)`，AUTOPAY 容量按 `wire.MaxDKVSRecordSize` 满负荷 record 计算。
7. tombstone、renewal、mailbox msg/share、blob manifest/chunk 是否不同费率。

推荐的最小生产接入：

- 第一版以 AUTOPAY verifier 为主：record proof 提供 contract/payer/payer_pubkey/key_hash/record_hash/expiry/size，节点读取 contract state 重新校验。
- miner 收益分配先由 AUTOPAY/Pool 合约内部规则处理，DKVS core 不计算收益。
- 主网 `AllowFreeLocal=false`，且没有主网默认 AUTOPAY 参数前不自动放行；测试网使用硬编码 AUTOPAY 默认参数，本地仍可显式开启 `FREE_LOCAL`。

代码接入点：

- `indexer/indexer/dkvs.FeeVerifier`
- `indexer/indexer/dkvs.AutopayFeeVerifier`
- `indexer/indexer/dkvs.RPCAutopayStateProvider`
- `indexer/indexer/dkvs.HTTPFeeVerifier`
- `indexer/indexer/dkvs.NetworkDefaultsForParams`
- `dkvs.Config.FeeVerifier`
- `indexer.Config.DKVS.FeeVerifier`
- `IndexerMgr.SetDKVSFeeVerifier`
- `FeeAnchorHash(record)` 是当前 fee proof 的 record hash 输入，避免 fee proof 自引用。

## 3. Checkpoint / Snapshot Anchor

当前阻塞：

- 已有非共识 checkpoint/snapshot API，用于节点视图对账、调试和 snapshot 校验。
- checkpoint/snapshot 当前是未签名的计算结果，不作为 `/sys/*` signed DKVS record 发布。
- embedded indexer 不持有系统私钥，也不自动签名发布 checkpoint/snapshot record。
- 未定义真实链上 anchor 交易或系统合约格式。

需要决策：

1. checkpoint anchor epoch 规则：沿用本地自动发布高度间隔，还是使用独立 anchor epoch。
2. checkpoint anchor publisher：当前 block miner、core node、bootstrap node、治理 signer，还是系统合约。
3. 链上 anchor signer 权限来源；私钥应由钱包、治理 signer 或外部签名服务持有，不应放在 embedded indexer。
4. 链上 anchor 载体：普通交易 opreturn、系统合约状态、coinbase 扩展，还是专用 DKVS anchor tx。
5. anchor 内容：只锚定 `active_record_root`，还是包含 height、count、namespace_roots、snapshot_hash。
6. anchor 失败时是否影响 DKVS 读写；建议第一版不影响，只记录告警。

推荐的最小生产接入：

- 第一版保留当前非共识 checkpoint/snapshot API，用于节点视图对账、调试和 snapshot 校验。
- 当前不提供 signed checkpoint/snapshot record 和外部 HTTP anchorer 适配器。
- 如果未来需要链上锚定，由外部服务或系统合约先定义链上交易/状态格式，再决定是否需要签名 envelope。
- 真实链上 anchor 作为后续协议升级，不改变当前交易/区块/共识语义。

代码接入点：

- `SystemVerifier`
- `HTTPSystemVerifier`
- `indexer.Config.DKVS.SystemVerifier`
- `indexer.Config.DKVS.SystemVerifierHTTPEndpoint`
- `IndexerMgr.SetDKVSSystemVerifier`
- `GET /v3/dkvs/checkpoint`
- `GET/POST /v3/dkvs/snapshot`

## 4. Production DID / Pool Samples

当前阻塞：

- 已有 REST API 文档、Go SDK client、应用级 helper、可执行 Go examples 和 PWA DKVS developer tool。
- PWA developer tool 只提交已签名 record JSON，不在前端臆造 DID owner、fee proof 或 signing flow。

需要决策：

1. 真实 DID resolver 服务地址和返回数据来源。
2. record 签名由哪个 wallet flow 提供：当前账户私钥、插件签名接口，还是 SDK helper。
3. fee proof 如何输入：测试网 AUTOPAY 默认 proof、本地手动 JSON、主网 AUTOPAY proof。
4. 主网 AUTOPAY proof 的构造、查询和错误展示方式。
5. 生产样本是否做成钱包恢复、mailbox、service authenticity，还是保留开发者工具。

推荐的最小生产接入：

- 当前先保留开发者工具页，只调用已有 DKVS REST API，不写业务入口。
- 在真实 DID resolver 和主网 AUTOPAY 参数未就绪前，UI 只提交已签名 record JSON 或执行只读查询；测试网可使用 SDK AUTOPAY helper 做端到端样本。

代码接入点：

- `sat20wallet/sdk/wallet/dkvs_client.go`
- `sat20wallet/sdk/wallet/dkvs_apps.go`
- `sat20wallet/pwa/apis/dkvs.ts`
- `sat20wallet/pwa/entrypoints/popup/pages/wallet/DKVSTool.vue`
- `satoshinet/docs/dkvs-pwa-api-examples.md`

## Decision Checklist

继续开发前建议按顺序确认：

1. DID resolver 采用哪个权威数据源和 API。
2. DID 中 DKVS signing key 的声明方式。
3. 主网 AUTOPAY 参数、收益规则，以及是否还需要 ONESHOT/LEASE。
4. Checkpoint 是否需要链上 anchor；如果需要，anchor 交易或系统合约格式是什么。
5. 生产 DID/mainnet fee 样本选择哪个应用流程。

确认 1-3 后，可以继续实现真实 `/name`、`/svc` 和主网 fee proof 生产接入。确认 4 后，可以实现 checkpoint 链上 anchor 协议。确认 5 后，可以把当前开发者工具升级为生产应用样本。
