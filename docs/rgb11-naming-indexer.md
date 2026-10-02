# RGB11 资产命名与 SatoshiNet DKVS 注册设计

## 1. 范围

本实现为后续 STP / Transcend 支持 RGB11 资产进入、退出聪网提供名称与注册基础设施。

本阶段实现：

- Primary Ordinals DID 的 DKVS personal 参数；
- RGB11 canonical name 的 DKVS 注册表；
- ContractID <-> AssetName 查询；
- CoreNode 内部注册入口；
- DKVS 同步、快照与恢复；
- Wallet SDK 所需的只读接口和测试基础。

本阶段不实现 RGB11 deposit / withdraw、private-channel splicing、RGB consignment ingress validation、L2 RGB11 余额 credit/debit，也不在 Transcend 插件中接入注册调用。

RGB11 naming 默认启用，不存在 Config.RGB11NamingSource 或其他 naming feature flag。

## 2. 身份模型

RGB11 严格资产身份始终是完整 ContractID。不再使用 fingerprint，也不引入 ContractID 的短 hash 作为第二套身份。

正式进入聪网后的 canonical AssetName：

~~~text
rgb11:f:<baseTicker>@<providerDID>
rgb11:f:<baseTicker>_2@<providerDID>
rgb11:f:<baseTicker>_3@<providerDID>
~~~

规则：

- protocol 固定为 rgb11；
- type 保留现有资产类型含义，不作为 provider 唯一性字段；
- provider 使用 Primary Ordinals DID；
- DID 最长 10 个字符；
- 第一个 ordinal 为 1，但名称中省略 _1；
- ordinal >= 2 时显示 _<ordinal>；
- ordinal namespace 为 (providerDID, normalized baseTicker)；
- ContractID 一旦获得 canonical AssetName，映射不可修改；
- ordinal 永不复用。

## 3. Primary DID

Primary DID 不使用单独的 L2 OP_RETURN Bind 交易，唯一存储位置为：

~~~text
/personal/<account_id>/primary_did
~~~

value 只保存 canonical DID 字符串，例如：

~~~text
alice
~~~

不重复保存 inscription_id、owner address、owner UTXO、sat、L1 height 等可由 L1 Indexer 查询的字段。

写入 primary_did 时验证：

1. record pubkey 对应当前 account；
2. DID 是合法 canonical Ordinals DID；
3. DID 长度 <= 10；
4. DID 可安全作为 DKVS canonical identifier；
5. L1 DID resolver 返回该 DID active；
6. L1 DID resolver 返回的当前 owner 与 record pubkey 派生的 P2TR 地址一致。

Primary DID 是账户当前身份选择，可以修改。Primary DID 修改不会重命名已经正式注册的 RGB11 资产。

## 4. RGB11 canonical registry

### 4.1 DKVS 是唯一持久化来源

RGB11 canonical name 不是 BaseIndexer 区块状态。

它不进入 BaseIndexer naming subindex，不跟随 L2 block checkpoint，也不在 SatoshiNet block replay 中重新计算或重新分配 ordinal。

唯一持久化来源是 DKVS。

节点恢复时应优先恢复/同步 DKVS；之后即使进行聪网区块重放，也不会重建或修改 RGB11 名称。

### 4.2 一资产一 key

每个正式注册使用一个 immutable DKVS key：

~~~text
/rgb11/<providerDID>/<baseTicker>/<ordinal>
~~~

value 只保存完整 32-byte ContractID。

例如：

~~~text
/rgb11/tether/usdt/1 -> <32-byte ContractID A>
/rgb11/tether/usdt/2 -> <32-byte ContractID B>
/rgb11/alice/usdt/1  -> <32-byte ContractID C>
~~~

对应：

~~~text
/rgb11/tether/usdt/1 => rgb11:f:usdt@tether
/rgb11/tether/usdt/2 => rgb11:f:usdt_2@tether
~~~

value 不重复保存 provider、ticker、ordinal、AssetName、Genesis address、Genesis outpoint、inscription 或 fingerprint。

### 4.3 不可变与 ordinal

RGB11 registry record：

- Seq = 1；
- TTL = 0；
- 不允许 tombstone；
- 不允许更新 value；
- 不允许覆盖既有 ordinal；
- 不允许删除；
- 不使用普通 DKVS fee proof。

同一 (providerDID, baseTicker) 下只能创建当前最后 ordinal + 1。不能跳号，ordinal 永不复用。

### 4.4 ContractID 全局唯一

完整 ContractID 在整个 /rgb11 namespace 中只能出现一次。

因此必须保持：

~~~text
ContractID -> exactly one AssetName
AssetName  -> exactly one ContractID
~~~

## 5. 谁负责注册

Wallet SDK 不允许直接写 /rgb11。普通 DKVS Put、DKVS CAS 和 Wallet API 都不能创建 canonical mapping。

/rgb11 必须由处理对应 transcend.tc 合约部署的 CoreNode 写入。

后续 Transcend 流程预期为：

~~~text
transcend.tc deploy
        |
        v
CoreNode validates deploy
        |
        v
resolve ContractID / base ticker / Genesis address
        |
        v
read /personal/<account>/primary_did
        |
        v
L1 Indexer verifies Owner(primary_did) == Genesis address
        |
        v
read current /rgb11/<provider>/<ticker>
        |
        v
allocate next ordinal
        |
        v
CoreNode signs immutable DKVS record
        |
        v
internal RGB11 registry write
~~~

Transcend 插件实际调用将在后续 STP/RGB ingress 工作中接入；本 PR 只提供聪网端能力。

## 6. CoreNode 内部写入口

SatoshiNet 提供本机内部路由：

~~~text
POST /v3/rgb11/register
~~~

规则：

- 只允许 loopback 本机调用；
- 不属于公开 Wallet DKVS API；
- 接收完整、已签名 DKVSRecord；
- record key 必须属于 /rgb11；
- record signer pubkey 必须是当前 SatoshiNet CoreNode；
- 最终调用 PutInternalRGB11Registry。

DKVS 内部写再次验证 record signature、canonical key、ContractID、Seq=1、TTL=0、无 FeeProof、ordinal 连续、key 不存在和 ContractID 全局未注册。

## 7. Transcend deploy payload

RGB11 naming 不向 transcend.tc deploy payload 增加 naming descriptor。

不重复携带 ContractID、provider DID、Genesis address、Genesis outpoint、ordinal 或 AssetName。

能从其他确定来源取得的数据，不重复写入 deploy payload。

## 8. DKVS 同步与恢复

RGB11 registry 属于 network-replicated DKVS 状态，但只有 CoreNode 内部业务路径可以创建。

canonical path：

~~~text
/rgb11/<provider>/<ticker>
~~~

其记录为：

~~~text
.../1
.../2
.../3
~~~

path snapshot 必须满足 ordinal 从 1 连续排列，不能缺号或乱序。

DKVS full path repair 现有协议要求 TrustedSource、ValidatorID 和 signed sync response。因此接收历史 /rgb11 snapshot 时，不需要动态要求原 record signer现在仍然是 CoreNode；历史注册不会因为 CoreNode 后续退出而失效。

单条 record 仍保留创建时的 CoreNode DKVS 签名。

SatoshiNet block replay 明确不创建、不删除、不重建、不重新排序任何 RGB11 naming record。

## 9. 查询接口

只读接口：

| Method | Route | 作用 |
|---|---|---|
| GET | /v3/rgb11/naming/status | naming source = DKVS |
| GET | /v3/rgb11/contract/:contractid | ContractID -> registration |
| GET | /v3/rgb11/name/:assetname | AssetName -> ContractID / registration |
| GET | /v3/rgb11/ordinal?provider=...&ticker=... | 当前 max ordinal |

Primary DID 直接通过 /personal/<account>/primary_did 读取，不从 RGB11 registry 复制查询。

## 10. Wallet SDK 边界

RGB11 尚未进入聪网时：

- 严格 identity = ContractID；
- local SDK name 可修改；
- 没有合格 DID 时默认 ticker@<GenesisAddress 后12位>；
- Primary DID 修改不会自动把本地名称变成 canonical name。

Wallet 可以写 /personal/<account>/primary_did，但不能写 /rgb11。

正式注册之后，Wallet 可以读取 CoreNode/DKVS 已产生的 canonical mapping 并本地缓存用于展示；本地缓存不是 authority。

## 11. 不变量

1. Primary DID 最长 10 字符。
2. /personal/<account>/primary_did 只能由 account 自身更新。
3. /rgb11 不能通过普通 DKVS 写 API 创建。
4. 每个 /rgb11/.../<ordinal> key 永久不可变。
5. ordinal 从 1 连续递增且不复用。
6. ContractID 全局只能注册一次。
7. AssetName 能确定唯一 ContractID。
8. fingerprint 不参与 RGB11 naming。
9. Wallet local name 不参与资产严格 identity。
10. block replay 不参与 RGB11 naming reconstruction。
11. DKVS 是 canonical mapping 的唯一持久化来源。

## 12. 测试覆盖

SatoshiNet：

- Primary DID <=10 成功；
- 11 字符 DID 拒绝；
- Primary DID owner 不匹配拒绝；
- 普通 DKVS Put 写 /rgb11 被拒绝；
- CoreNode internal writer 成功；
- 非 CoreNode internal HTTP writer 拒绝；
- 非 loopback writer 拒绝；
- ordinal 连续；
- ordinal gap 拒绝；
- overwrite 拒绝；
- ContractID 跨 namespace 重复拒绝；
- DKVS path snapshot 在 fresh node 恢复同一 mapping；
- 恢复不依赖 SatoshiNet block replay；
- race test；
- full indexer tests；
- full node compile。

Wallet SDK：

- ContractID 与 local display name 分离；
- local rename 不影响余额、proof、transfer identity；
- primary_did 真实 DKVS 写入与读取；
- 11 字符 DID 拒绝；
- 非当前 owner DID 拒绝；
- Primary DID 可替换；
- 正式 CoreNode 注册前 local RGB11 name 仍可修改；
- Wallet 无 /rgb11 写入口。

## 13. 后续 STP / Transcend 接入

后续 RGB11 ingress 实现中，Transcend CoreNode 在处理 transcend.tc 部署时调用本 PR 提供的 internal registration capability。

下一阶段需要继续实现：

- 从真实 RGB Contract / consignment 验证 ContractID；
- 确定 Genesis provider address；
- 验证 Primary DID 当前 L1 owner 与 Genesis address 一致；
- 生成并签署 /rgb11 immutable record；
- 定义注册失败与合约部署失败之间的事务边界；
- deposit / withdraw；
- L2 balance credit/debit。

这些不属于本 PR。
