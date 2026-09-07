# SatoshiNet btcd 差异功能审查整改与 PoS V2 实施计划

更新时间：2026-08-23  
适用仓库：`sat20-labs/satoshinet`，以及需要联动的 `sat20-labs/indexer`、`sat20-labs/sat20wallet`  
文档状态：**设计已对齐，等待当前测试轮次结束后实施**  
文档用途：供下一次开发 session 直接读取并继续实施；与此前聊天中的阶段性总结冲突时，以本文为准。

---

## 1. 执行约束

1. 当前测试轮次仍在进行，本轮只新增本文档，不修改生产代码、不替换节点二进制、不重启测试网络。
2. 等当前测试轮次完整结束后，保存以下基线再开始实施：
   - SatoshiNet、indexer、wallet SDK 的 commit；
   - 所有节点的 tip height/hash；
   - 测试日志；
   - 必要的数据库备份；
   - 当前工作区未提交修改清单。
3. 修改只放在 git changes/unstaged 中；除非用户明确要求，不得 stage，不得 commit，不得 push。
4. 网络级 E2E 必须串行执行，不能并行启动多个 SatoshiNet 节点测试套件。
5. 聪网主网当前仍是 2026 年 4 月底版本：没有智能合约，没有 DKVS。任何新共识规则必须通过未来激活高度启用，不得回溯要求历史区块满足新格式。
6. 对资产规则的任何收紧，必须先扫描聪网主网历史交易、当前 UTXO、spend journal 和 Anchor 数据。发现任何历史不兼容数据时，立即停止该项修改并重新讨论兼容方案。
7. SegWit v0 资产 sighash 保持现状。除非后续确认存在可直接盗取资产、绕过签名或增发资产的严重漏洞，否则不得修改其共识摘要算法。
8. 正式发布时固定 SatoshiNet 与 indexer 的 commit/tag；本轮不搬迁共识资产类型，也不提升现有 UTXO/spend-journal DB version。

---

## 2. 已确认的总体架构决策

### 2.1 PoS V2

PoS V2 采用未来激活高度，整体启用以下规则：

- 完整区块 proposal digest；
- producer signature；
- Bootstrap finality certificate；
- Miner → Core → Bootstrap 替补出块；
- Bootstrap 一高度一签名的持久化锁；
- direct-tip only；
- 禁止 side chain、PoS V2 orphan 延伸和 automatic reorg；
- 不再使用累计 PoW work 做 fork choice；
- PoS V2 固定规范 Bits；
- 零 BTC 区块补贴；
- `BFFastAdd` 不能跳过 PoS V2 校验。

### 2.2 No-reorg 目标

PoS V2 激活后，聪网把已接入的区块视为最终确认区块。网络分区时，没有对应 Bootstrap finalizer 的一侧应停止出块，而不是生成可在恢复后参与链重组的侧链。

### 2.3 EVM source metadata

EVM 合约源码不保存在 contract indexer。规范存储位置为：

```text
/blob/evm/source/<contract_address>
```

其中 `<contract_address>` 是 DeployTx 成功后生成的、规范编码的 SatoshiNet EVM 合约地址。路径中的地址直接用于查找，不再做额外 hash 映射。

DeployTx 已支付的 deploy fee 同时包含该合约一个永久源码 Blob 的一次性存储费用，不再收取 DKVS AUTOPAY、LEASE 或独立存储费。

任何人都可以提交源码，但**节点只有在确定性编译和链上 bytecode 验证全部通过后，才允许源码进入该规范路径**。该路径中存在的源码即表示“已验证为该合约的源码表示”，不得保存未经验证的候选源码。

### 2.4 明确不修改

- 不增加通用 `ValidatorId` 握手认证；
- 不修改 SegWit v0 资产 sighash；
- 不提升 UTXO/spend-journal DB version；
- 不将 `AssetName`、`AssetInfo`、`TxAssets` 从 indexer 包迁入 SatoshiNet；
- 不实现 PoS V2 候选分支排序机视图；
- 不实现 PoS V2 候选分支 Anchor set；
- DKVS FREE_LOCAL 的 7200 blocks 数值不改，只修正注释；
- 本轮不统一 mempool 浏览器、目录名和 RPC chain name 等网络命名。

---

## 3. PoS V2 与最终性

## 3.1 激活参数

在 `chaincfg.Params` 中增加：

```go
type Params struct {
    // ...
    PosV2ActivationHeight int32
}
```

分别配置：

```go
MainNetParams.PosV2ActivationHeight
TestNetParams.PosV2ActivationHeight
```

参数是编译进 chain params 的共识参数，不允许节点通过普通运行参数各自指定。

规则：

```text
height < H   -> PoS V1
height >= H  -> PoS V2
```

区块 `H` 是第一个 PoS V2 区块，`H-1` 是最后一个 PoS V1 区块。

### 主网

当前先设置为未激活值，例如：

```go
math.MaxInt32
```

正式发布前再设置明确的未来高度。发布时同时固化：

```text
H-1 / hash(H-1)
```

作为 release checkpoint，确保所有节点从同一个父块进入 PoS V2。

### 测试网

测试网也必须设置明确的 PoS V2 激活高度，但不能在当前测试轮次中途设置。

下一轮测试前执行：

1. 停止全部测试网出块节点；
2. 使用当前 PoS V1 版本把所有节点回滚到相同高度 `R`；
3. 确认所有节点的 `R/hash(R)` 完全一致；
4. 设置 `TestNetParams.PosV2ActivationHeight = Htest`；
5. 要求 `Htest > R`；
6. 所有 Bootstrap、Core、Miner 使用相同 commit、相同二进制、相同激活高度；
7. 在达到 `Htest` 之前完成全部升级；
8. 从 `Htest` 开始执行 PoS V2/no-reorg 验收。

可以选择 `Htest = R + 1`，也可以保留少量 V1 区块作为观察窗口。具体高度在下一轮测试准备时确定。

必须遵循：

```text
先在 V1 下回滚
-> 再设置并部署 V2 激活高度
```

不能先进入 PoS V2，再尝试回滚已经最终确认的 V2 区块。

## 3.2 Producer proposal digest

PoS V1 当前只签 `height-nonce`。PoS V2 必须签完整区块 proposal。

为避免签名写入 coinbase 后改变 Merkle root 和 block hash 造成循环，定义：

```text
PosProposalDigestV1
```

规范计算建议：

1. 深拷贝候选区块；
2. 将 coinbase 中 PoS certificate/signature 字段替换为规范空值；
3. 保留 coinbase 的所有经济输出和 combined state root commitment；
4. 重新计算副本的 Merkle root；
5. 使用 WitnessEncoding 序列化规范副本；
6. 计算：

```text
SHA256d(
    "satoshinet:pos:block:v1"
    || network_magic
    || canonical_block_without_pos_signatures
)
```

该 digest 必须绑定：

- network；
- height；
- previous block hash；
- timestamp；
- Bits；
- header nonce；
- 所有交易和 witness；
- coinbase 支付输出；
- gas/fee 归集；
- combined contract state root。

## 3.3 区块证书

PoS V2 区块至少包含：

```text
ProducerSignature
FinalizerSignature
```

建议继续放在 coinbase `SignatureScript` 的版本化结构中：

```text
<height>
<extra_nonce>
<pos_version=2>
<producer_signature>
<finalizer_signature>
```

DER 签名可能使脚本超过当前 100 bytes 上限，PoS V2 激活后应使用明确的新上限，例如 256 bytes。PoS V1 历史区块继续沿用旧限制。

## 3.4 原定生产者与替补层级

对于高度 `H`：

- `expectedProducer`：排序机确定的原定节点；
- `actualProducer`：实际出块者；
- `finalizer`：`expectedProducer` 所在路径最顶层 Bootstrap。

允许的 `actualProducer`：

```text
expectedProducer
expectedProducer.Father
expectedProducer.Father.Father
```

按节点类型具体为：

```text
Miner slot      -> Miner / Core / Bootstrap
Core slot       -> Core / Bootstrap
Bootstrap slot  -> Bootstrap
```

不引入 BFT committee 或复杂 timeout certificate。现有：

```text
MinerInterval
PreWarningInterval
peer connection state
```

继续作为 Bootstrap 是否签发替补 proposal 的本地策略。普通节点最终只验证 Bootstrap finality certificate，不独立推断墙钟超时。

## 3.5 Bootstrap finality certificate

现有 `MsgMineBlock` 审核链路继续复用：

```text
Miner -> Core -> Bootstrap
Core -> Bootstrap
Bootstrap -> Core review
```

流程：

1. actual producer 构造完整 proposal；
2. actual producer 签 `PosProposalDigestV1`；
3. proposal 通过现有 `MsgMineBlock` 发给上级；
4. Bootstrap 验证 direct-tip、排序、替补层级、交易、资产、Anchor、合约和 state root；
5. Bootstrap 签同一个 `PosProposalDigestV1`；
6. `MsgMineAck` 返回 `FinalizerSignature`；
7. producer 将两个签名写入最终 coinbase，重新计算 Merkle root；
8. 最终区块通过普通 block P2P 路径广播。

所有节点根据链上排序机状态确定 producer/finalizer 公钥，不把 `peer.ValidatorId` 当作共识身份依据。

## 3.6 Bootstrap 一高度一签名锁

Bootstrap 返回 finalizer 签名前必须原子持久化：

```text
height
prev_block_hash
proposal_digest
expected_producer
actual_producer
fallback_level
finalizer_signature
```

要求：

- 相同 proposal 重试：返回相同签名；
- 同一高度不同 proposal：拒绝；
- 必须先落盘，再发送 ACK；
- Bootstrap 重启后恢复；
- 已签名 proposal 即使未成功传播，也不得改签另一块；
- 失败时宁可停链，也不能通过双签恢复活性。

这是 no-reorg 的核心安全边界。

## 3.7 Direct-tip only 与禁止 reorg

PoS V2 激活后：

```go
block.Header.PrevBlock == bestChain.Tip().Hash
```

必须成立。

否则：

- parent 已知但不是 tip：返回 `ErrForkNotAllowed`；
- parent 未知：返回缺失父块/不可接入错误；
- 不进入 side-chain block index；
- 不进入 PoS V2 orphan pool；
- 不持久化为侧链区块；
- 不参与 fork choice；
- 不触发 reorg。

至少在以下位置增加防御：

- `blockchain.ProcessBlock`；
- `maybeAcceptBlock`；
- `connectBestChain`；
- `reorganizeChain`；
- `InvalidateBlock`；
- `ReconsiderBlock`。

生产网络不得 detach PoS V2 区块。测试工具如确需回滚，只能在 PoS V2 激活前完成，或通过显式、离线、全网协调的维护流程处理，不能作为普通节点运行能力。

## 3.8 停止使用 chainwork fork choice

为避免数据库迁移：

- 保留 `blockNode.workSum` 字段；
- 保留历史 work 数据；
- PoS V2 区块使用固定规范 `Bits`，建议 `PowLimitBits`；
- 不再进入任何按 `workSum` 比较侧链的路径。

PoS V2 fork choice 实际上只有：

```text
带有效 producer/finalizer certificate 的当前 tip 直接子块
```

Bitcoin difficulty retarget 只保留给历史 V1 区块或旧测试，不再作为 PoS V2 安全权重。

## 3.9 `BFFastAdd` 不得绕过 PoS V2

以下检查必须始终执行：

- PoS V2 coinbase/certificate 格式；
- producer signature；
- finalizer signature；
- expected producer；
- actual producer 替补层级；
- finalizer 身份；
- direct-tip；
- 固定 Bits；
- 零补贴；
- combined state root。

## 3.10 零 BTC 补贴

PoS V2 开始后，coinbase BTC 输出上限只能是：

```text
普通交易 BTC fees
```

不得再包含：

```go
CalcBlockSubsidy(height)
```

资产 gas fee继续按现有资产 fee 规则处理。PoS V1 历史区块不回溯应用本规则。

## 3.11 排序机 direct-parent readiness

PoS V2 不实现候选分支排序机视图。验证区块前必须保证：

```text
IndexerMgr internal tip height/hash
==
candidate parent height/hash
```

然后才允许计算：

- expected producer；
- father/grandfather；
- top Bootstrap finalizer。

排序机 readiness 应独立于“是否启用合约”；即使主网尚未启用智能合约，也必须满足该 direct-parent 不变量。

## 3.12 合约激活顺序

主网要求：

```text
ContractActivationHeight >= PosV2ActivationHeight
```

避免出现智能合约已经产生状态，但链仍允许常规 reorg 的生产阶段。

---

## 4. 资产共识安全

以下修改前必须先扫描主网历史数据。任何一项发现历史不兼容记录时，停止修改并重新讨论未来激活规则。

## 4.1 禁止负数和零数量

所有资产项要求：

```text
Amount > 0
```

覆盖：

- 普通交易输出；
- Anchor 输出；
- coinbase fee asset 输出；
- 合约 result 输出；
- UTXO/spend journal 解码后的共识校验。

禁止：

- 负资产；
- 零资产；
- nil Decimal；
- 通过正负项抵消绕过资产守恒。

## 4.2 BindingSat 一致性

要求：

- 同一 AssetName 的 `BindingSat` 不得在转账中任意改变；
- 与 ticker 注册信息一致；
- 同一输出中同名资产不能出现不同 `BindingSat`；
- 输出 sats 必须满足绑定资产所需聪数：

```text
TxOut.Value >= required_binding_sats
```

## 4.3 资产列表 canonical form

每个 `TxOut.Assets` 必须：

- AssetName 唯一；
- 严格排序；
- 无重复；
- 无空字段；
- Decimal 合法；
- BindingSat 不溢出；
- 具有唯一 canonical serialization。

## 4.4 AssetName 严格三段

聪网只允许：

```text
protocol:type:ticker
```

要求：

- 恰好三个非空字段；
- 恰好两个冒号；
- 字段内部不能再包含冒号；
- 不支持一段、两段、四段 fallback；
- 不自动推断默认 type。

以下格式仍合法，因为 `#`、`@` 位于 ticker 内：

```text
rgb11:f:usdt#k7m3q9x2d4
rgb11:f:usdt@k7m3q9x2d4
```

统一应用于：

- wire；
- mempool；
- blockchain；
- indexer；
- contract；
- RPC；
- wallet SDK。

不要在确认历史数据前无条件把严格校验放入无法感知高度的历史 wire decode。

## 4.5 资源上限

增加明确上限：

- 每个输出最大资产项数；
- Protocol 最大长度；
- Type 最大长度；
- Ticker 最大长度；
- Decimal 文本最大长度；
- 单输出资产总编码大小；
- BindingSat 范围。

## 4.6 SegWit v0 sighash

保持当前算法，不修改历史签名摘要。

仅增加：

- 固定 sighash vector；
- wallet SDK 从可信 UTXO 数据获取 prevout assets；
- 禁止未来无意改变已有算法。

---

## 5. 智能合约状态

## 5.1 每个激活后区块都承诺 combined state root

合约激活以后，无论区块是否有合约 activity，都必须有且只有一个 combined state root commitment。

计算：

- 有模块 activity：使用模块 post-state root；
- 无模块 activity：使用模块 parent-state root；
- 模块从未产生状态：使用零 root；
- 最后组合 Template、EVM、Agent root。

无 activity 区块承诺父状态不变后的 combined root。缺失、重复或错误 commitment 均拒绝。

## 5.2 状态大小在 validation 阶段检查

当前 16 MiB 限制不能只在数据库持久化时执行。Template、EVM、Agent 的 post-state 必须在 block validation 阶段执行完全相同的确定性大小检查，避免“共识验证通过、DB 提交失败”。

## 5.3 KnownValid 与 post-state

如果 block 被标记 KnownValid，但 transient post-state 已丢失：

- 有 post-state：提交；
- 没有 post-state：重新执行合约；
- 不能静默跳过状态写入。

## 5.4 状态快照清理

需要设计：

- 最近测试/诊断窗口；
- 当前 tip state；
- finalized checkpoint；
- 旧 block snapshot pruning。

PoS V2 生产环境不允许 reorg，可采用较小历史快照窗口；测试网可保留更长诊断窗口。

---

## 6. EVM source metadata：规范系统 Blob

## 6.1 核心保证

规范路径：

```text
/blob/evm/source/<contract_address>
```

系统必须保证：

> 任意被接受并保存到该路径的 Solidity source package，按其声明且受支持的确定性编译配置重新编译后，生成的 Deploy init code 与链上 DeployTx 的 `ContractContent` 完全一致；使用该 init code 在 DeployTx 的准确链上执行上下文中重放部署后，得到的 contract address 和 runtime bytecode 与链上已提交状态完全一致。

因此，该路径不保存“待验证源码”“用户声称已验证的源码”或仅靠 hash 声明的源码。验证不通过即拒绝写入。

需要明确一个客观边界：bytecode 不能唯一反推出发布者最初输入的文本。不同注释、空白或其他不影响编译结果的源码文本，理论上可能生成相同 bytecode。系统能够保证的是：

```text
保存的源码在固定编译流程下，与该合约部署的 init/runtime bytecode 精确等价
```

这就是该路径中“该合约源码”的规范含义。

## 6.2 Key 规则

`<contract_address>` 必须是 DeployTx 成功后返回的规范 SatoshiNet EVM 合约地址：

- 使用统一 contract address decoder 校验；
- 必须是 EVM contract type；
- 必须使用当前 network prefix；
- 必须以 canonical lower-case 编码重新输出后与路径完全一致；
- 不允许使用原始 `0x...` EVM address 替代；
- 不允许客户端另选 blob key。

逻辑映射：

```text
contract address
-> /blob/evm/source/<contract_address>
```

一笔成功 DeployTx 只对应一个规范源码槽位。

## 6.3 DKVS path mode

该路径是特殊系统 Blob，不使用普通：

```text
/blob/<account_id>/<blob_key>
```

的 owner-exclusive 规则。

新增专用模式，例如：

```text
VerifiedPublicWriteOnce
```

规则：

- 任何有效签名者都可以提交首次写入；
- 不检查 signer 是否为 deployer/caller；
- 写权限只取决于 DeployTx entitlement 和源码 bytecode 验证；
- 首次写入必须 `expect_absent`、`Seq=1`；
- 完全相同 record/hash 可以幂等重试；
- 已存在不同内容时默认拒绝覆盖；
- 禁止 tombstone；
- 不允许任意 writer 免费反复改写规范源码。

采用 write-once 是为了避免“任何人可写 + 无额外费用”形成无限覆盖、P2P 修复和编译 DoS。若未来确需更换规范源码，应单独设计治理/authority replacement 流程，不在本次协议中开放普通更新。

## 6.4 Source package

建议使用版本化、可确定编译的 JSON envelope：

```json
{
  "version": 1,
  "language": "Solidity",
  "contractAddress": "...",
  "deployTxid": "...",
  "contractName": "...",
  "source": "...",
  "compilerConfig": {
    "solcVersion": "0.8.30",
    "evmVersion": "paris",
    "optimizer": {
      "enabled": true,
      "runs": 200
    },
    "metadata": {
      "bytecodeHash": "none"
    },
    "singleFileOnly": true,
    "allowImports": false
  },
  "constructorArgs": "",
  "abi": [],
  "initCodeHash": "...",
  "runtimeCodeHash": "..."
}
```

要求：

- `contractAddress` 与 path 一致；
- `deployTxid` 指向对应 DeployTx；
- `constructorArgs` 是不带 `0x` 的 canonical lower-case hex；
- `abi` 必须与 compiler output 的 canonical ABI 一致，不能由客户端任意伪造；
- `initCodeHash`、`runtimeCodeHash` 必须由节点重新计算并比对；
- 不接受客户端控制的 `verified=true` 作为事实来源；
- “路径中存在有效记录”本身即表示 verified。

若节点需要在 HTTP 响应中展示：

```text
verified
verifiedAt
verifiedHeight
compilerBinaryHash
```

这些字段应由节点验证结果派生，不能直接信任 Blob 内声明。

## 6.5 确定性编译环境

初始版本采用严格、可重现配置：

- Solidity 单文件；
- 禁止 imports；
- 禁止远程 URL；
- 禁止读取节点任意文件；
- `solc --standard-json`；
- `solcVersion` 必须在节点白名单中；
- 每个支持版本固定可执行文件 hash；
- `evmVersion`、optimizer、runs、metadata 设置全部进入编译输入；
- 初始默认与当前 API 对齐：`solc 0.8.30`、`paris`、optimizer enabled、runs 200、`bytecodeHash=none`；
- compiler output 若包含 unresolved link references，拒绝；
- compiler warning 可返回，但 compiler error 必须拒绝；
- 编译运行在无网络、有限 CPU、有限内存、有限输出、有限时长的 sandbox 中；
- compiler crash、timeout、OOM 或输出过大一律 fail closed。

后续若支持多文件/import，必须先定义完整 source bundle、路径 canonicalization、依赖 hash 和 compiler input hash；不得直接开放节点文件系统或网络 import。

## 6.6 写入验证流程

一次 source write 必须按以下顺序执行：

### A. 基础输入校验

1. 解析 `/blob/evm/source/<contract_address>`；
2. 校验 source package version、大小和 JSON canonical form；
3. 校验 DeployTx txid 格式；
4. 校验 constructor args hex；
5. 校验 compiler config 在支持范围内；
6. 校验 record signature 和 DKVS 基础格式；
7. 校验 `TTL=0`；
8. 校验 `Seq=1`、`expect_absent`。

### B. 查询链上 DeployTx

从 canonical blockchain/contract execution 读取，而不是只信任普通 indexer summary：

1. DeployTx 已确认；
2. 类型是 EVM DeployTx；
3. DeployTx 执行结果为 success；
4. deploy fee 已按共识支付；
5. DeployTx 生成的 SatoshiNet contract address 与 path 完全一致；
6. DeployTx 的 `ContractContent` 可完整提取；
7. 对应 block 和 EVM committed state 可读取。

因为主网合约激活不得早于 PoS V2，生产环境中的成功 DeployTx 已处于 no-reorg/finalized 链，不需要额外等待可被 reorg 的确认窗口。

### C. 编译源码

节点使用声明的精确 compiler config 编译：

```text
source + contractName + compilerConfig
```

提取：

- creation bytecode；
- deployed bytecode template；
- ABI；
- immutable references；
- link references；
- compiler diagnostics。

要求：

- `contractName` 必须唯一选中一个输出 contract；
- creation bytecode 非空；
- 无 unresolved libraries/link placeholders；
- ABI canonical JSON 与 package 中 ABI 一致。

### D. 精确比对 Deploy init code

构造：

```text
compiled_init_code = creation_bytecode || constructor_args
```

必须满足：

```text
compiled_init_code == DeployTx.ContractContent
```

必须是逐字节完全相等，不能只比较长度、metadata 前缀或模糊 hash。

该检查证明：链上真正执行的部署 init code 就是该 source/config/constructor args 的编译结果。

### E. 精确重放 runtime bytecode

为了处理 Solidity immutable、constructor 环境访问、同区块前序交易和其他会使 runtime code 与静态 `deployedBytecode.object` 不完全相同的情况，不能只把 compiler 的静态 runtime template 与链上 code 做简单比较。

必须使用准确链上上下文进行确定性重放：

1. 加载 DeployTx 所在 block 的 parent EVM state；
2. 按 canonical 顺序重放该 block 中 DeployTx 之前的所有相关 EVM work/result 状态转换，得到 DeployTx 的准确 pre-state；
3. 使用链上实际：
   - caller；
   - deploy nonce；
   - funding output；
   - gas limit；
   - block number；
   - block time；
   - parent hash；
   - chain ID；
   - coinbase；
   - 同一 EVM chain config/precompiles；
4. 在隔离 state clone 中执行 `compiled_init_code`；
5. 要求重放结果 status 为 success；
6. 要求重放生成的 contract address 与 path 一致；
7. 读取重放后的 runtime code；
8. 读取 canonical committed EVM state 中该合约的 runtime code；
9. 要求两者逐字节完全一致。

必须满足：

```text
replayed_runtime_code == committed_runtime_code
```

若当前实现暂时无法获得准确 parent state、同区块前序状态或完整 block context，则源码写入必须失败关闭，不能退化成“只比较一个客户端提供的 runtime hash”。

### F. 派生字段校验

节点重新计算：

```text
initCodeHash
runtimeCodeHash
canonical ABI
compiler input hash
compiler binary hash
```

若 source package 携带相应字段，必须完全一致。任何不一致均拒绝。

### G. 原子写入

只有 A-F 全部通过后，才允许 DKVS `expect_absent` 写入：

```text
/blob/evm/source/<contract_address>
```

验证过程中不得先占用 key、不得保存临时未验证 value、不得向 P2P 广播。

## 6.7 费用语义

DeployTx 的正常 deploy fee 自动授予：

```text
一个 contract address 对应的永久 EVM source Blob 槽位
```

规则：

- 不额外支付；
- 不使用 AUTOPAY；
- 不使用 LEASE；
- `TTL=0`；
- 最大 Blob value 受固定上限约束；
- entitlement 只能用于路径中对应的 contract address；
- DeployTx 不能用于普通 `/blob/<account_id>/...`；
- DeployTx 不能用于另一合约地址；
- 由于路径唯一且 write-once，不需要额外维护“已消费多次”的独立计数；record 已存在即表示该槽位已使用。

可以复用 `ONESHOT` fee proof 表达：

```text
PaymentTxID  = deploy txid
PoolContract = contract address
```

但验证逻辑必须是专用 DeployTx source verifier，不能只检查字段非空。

## 6.8 Writer 身份

写入者可以不是：

- deployer；
- caller；
- 合约 owner；
- 某个固定 service account。

节点仍验证 DKVS record signature，以保证传输完整性和请求可追踪，但 source permission 不根据 signer 身份判定。

安全性来自：

```text
成功 DeployTx entitlement
+ exact compile/init-code match
+ exact runtime replay match
+ write-once
```

## 6.9 Contract indexer 与 API 边界

从 contract indexer 删除/停用：

```text
contract:v1:evm:source:*
```

Contract indexer只保存链上确定数据：

- contract address；
- DeployTx txid；
- deploy height；
- caller/deployer；
- deploy result；
- runtime bytecode/hash；
- invoke/result history。

HTTP 查询可以保留现有体验：

```text
GET /v3/contracts/:contract/evm/source
```

内部改为直接读取：

```text
/blob/evm/source/<contract_address>
```

提交接口可以继续使用类似：

```text
POST /v3/contracts/:contract/evm/source
```

但其语义变为：

```text
接收 source package
-> 编译与链上验证
-> 构造/校验 DKVS write
-> 写入规范系统 Blob
```

不得再直接覆盖本地 contract index DB。

## 6.10 EVM source 测试要求

至少覆盖：

1. 正确源码、正确 compiler config、正确 constructor args：接受；
2. 错误源码：拒绝；
3. 正确源码但错误 contractName：拒绝；
4. 正确源码但错误 solc version/settings：若 init code 不一致则拒绝；
5. 正确 creation code 但错误 constructor args：拒绝；
6. DeployTx 未确认：拒绝；
7. DeployTx 执行失败/revert/out-of-gas：拒绝；
8. path contract address 与 deploy result 不同：拒绝；
9. DeployTx proof 用于另一合约：拒绝；
10. DeployTx proof 用于普通 Blob：拒绝；
11. writer 与 deployer 不同：仍可接受；
12. client 自称 `verified=true` 但 bytecode 不匹配：拒绝；
13. ABI 与 compiler output 不同：拒绝；
14. initCodeHash/runtimeCodeHash 伪造：拒绝；
15. constructor 使用 immutable：runtime replay 后精确匹配；
16. constructor 读取 block context：使用准确上下文后精确匹配；
17. DeployTx 前同区块已有 EVM 状态变化：按前序状态重放后匹配；
18. unresolved library link：拒绝；
19. compiler timeout/OOM/crash：拒绝且不占 key；
20. 首次有效写入后，不同内容覆盖：拒绝；
21. 完全相同 record/hash 重试：幂等成功；
22. tombstone：拒绝；
23. 超过 Blob 上限：拒绝；
24. GET 按 contract address 直接返回已验证源码。

---

## 7. DKVS path snapshot 安全

## 7.1 Snapshot authority

当前 snapshot 已有 response signature，但不能仅以“peer 声明了非空 ValidatorId”作为 destructive snapshot authority。

完整 path snapshot 只允许来自：

- 链上已登记的 Core/Bootstrap；
- 或显式配置的 DKVS mirror authority。

## 7.2 不增加通用握手认证

`ValidatorId` 继续用于：

- peer 路由；
- 选择待验证公钥；
- 日志。

Snapshot 验证顺序：

1. 从 `ValidatorId` 得到公钥；
2. 验证 snapshot response signature；
3. 验证该公钥属于授权 Core/Bootstrap/mirror authority；
4. 重算 records、delete floors、StateRoot、count、size；
5. 原子替换 path。

## 7.3 普通 peer 只能触发 hint

普通 peer 的合法 DKVS record 可以触发：

```text
path diverged / repair required
```

但不能成为 destructive snapshot source。节点必须改为向授权 authority 请求完整 path snapshot。

## 7.4 FREE_LOCAL 注释

保留：

```text
7200 blocks
```

注释改为：

```text
7200 个区块；实际墙钟时长取决于真实出块速度，不保证等于一天。
```

---

## 8. 内嵌 indexer 与网络判断

## 8.1 Stake/unstake panic

修复：

- `handleStakeAssetV2` 中反向的 `channelMap` 判断；
- `Assets.Find()` 失败后解引用 nil；
- `removeMinerNode` 同类 nil dereference；
- 合法或恶意 OP_RETURN 不得让节点永久 panic 在某高度。

## 8.2 BaseIndexer Clone

对以下可变对象执行真正深拷贝：

- `tickInfoMap`；
- `channelMap`；
- `utxoIndex.Index`；
- ascend/descend/ledger/event/referrer maps；
- 其他会在 live compiling 中继续修改的指针对象。

`Clone(true)` 不得在持有 `RLock` 时修改 live `AddressValueV2.Op`。

## 8.3 主网判断

最小修复：

```go
func (b *IndexerMgr) IsMainnet() bool {
    return b.chaincfgParam != nil &&
        b.chaincfgParam.Net == wire.MainNet
}
```

本轮不统一其他组件的 network name。

## 8.4 Channel state event 并发保护

`RecordChannelStateEvent` 修改共享 map 时增加正确 mutex。生产禁用规则依赖修复后的 `IsMainnet()`。

---

## 9. 挖矿模板与提交

## 9.1 `SubmitNewBlock`

当前不能忽略 `submitBlock()` 的 bool 结果。修改后：

- stale：返回 error；
- orphan：返回 error；
- rule rejection：返回 error；
- 只有区块真正接入主链才返回 hash/height success。

## 9.2 Anchor/DeAnchor 模板资源

Anchor 和 DeAnchor 不能直接 append 绕过：

- block weight；
- sigops；
- fee/fee assets；
- UTXO view；
- double-spend；
- dependency ordering；
- parent/child 排序。

应统一进入模板选择流程，或实现完全等价的专用资源检查。

PoS V2 禁止侧链后，不再额外实现候选分支 Anchor set。

---

## 10. 测试与验证矩阵

## 10.1 PoS V2

至少覆盖：

- 激活前 V1 区块有效；
- 激活高度无 certificate 拒绝；
- 正确 producer/finalizer 接受；
- Miner、Core、Bootstrap 替补；
- 非法替补层级拒绝；
- Bootstrap 同高度第二个 digest 拒绝；
- approval lock 重启恢复；
- 非 tip parent 拒绝；
- PoS V2 orphan/side chain 不落盘；
- 更高 chainwork 不能触发 reorg；
- `BFFastAdd` 不能绕过；
- 零补贴；
- 落后节点按顺序同步；
- 网络分区后无 finalizer 一侧停链，恢复后顺序追链。

## 10.2 资产

至少覆盖：

- `-1 A + 2 A` 负资产增发；
- 零资产；
- 同名重复；
- 非排序列表；
- BindingSat 变更；
- sats 不足以承载绑定资产；
- coinbase 正负抵消；
- 超多资产项；
- 超长字段；
- 严格三段 AssetName；
- 历史主网数据扫描报告。

## 10.3 合约

至少覆盖：

- 无 activity 区块的父 combined root；
- 缺失/重复/错误 state root；
- validation 阶段状态过大；
- KnownValid post-state 丢失后重执行；
- state snapshot pruning；
- mixed Template/EVM/Agent；
- 更新不可编译的 legacy EVM E2E。

## 10.4 DKVS

至少覆盖：

- 未授权 snapshot source 拒绝；
- 授权 Core/Bootstrap 接受；
- 自声明 ValidatorId 但不在 authority 集合中拒绝；
- hint peer 与 snapshot authority 分离；
- EVM source 特殊 path、fee、compile、runtime replay 和 write-once 测试。

## 10.5 长测试基础设施

修复：

```text
日志已经退出
但 status 仍为 RUNNING
```

确保：

- Go test 退出后写入 PASSED/FAILED；
- tail 文件生成；
- MCP 请求超时不覆盖真实终态；
- 网络 E2E 串行执行。

---

## 11. 推荐实施顺序

### 阶段 0：结束当前测试

1. 冻结代码；
2. 完成本轮测试；
3. 保存日志、commit、tip 和 DB 备份；
4. 修复测试 supervisor 终态记录问题。

### 阶段 1：无历史格式影响的直接修复

1. `IsMainnet()`；
2. channel state event mutex；
3. stake/unstake panic；
4. BaseIndexer Clone；
5. `SubmitNewBlock` 返回值；
6. Anchor/DeAnchor 模板资源；
7. DKVS TTL 注释；
8. DKVS snapshot authority。

### 阶段 2：资产历史扫描与安全规则

1. 编写主网数据扫描工具；
2. 输出资产规范兼容报告；
3. 若全部兼容，再落地负数、零值、重复、排序、BindingSat、严格三段和资源上限；
4. 若发现不兼容，停止并重新设计未来激活规则。

### 阶段 3：PoS V2

1. 增加 activation params；
2. proposal digest；
3. producer/finalizer certificate；
4. approval lock；
5. direct-tip/no-reorg；
6. 停用 chainwork fork choice；
7. 固定 Bits；
8. 零补贴；
9. direct-parent indexer readiness；
10. 多节点 E2E。

主网高度保持未激活。

### 阶段 4：合约状态与 EVM source

1. 每块 combined root；
2. validation 阶段状态大小；
3. KnownValid/post-state；
4. state pruning；
5. 从 contract indexer 移除 source metadata；
6. 实现 `/blob/evm/source/<contract_address>`；
7. 实现 pinned solc sandbox；
8. 实现 exact init code 比对；
9. 实现准确链上上下文 runtime replay；
10. 实现 source API 和 E2E。

### 阶段 5：下一轮测试网

1. 使用 V1 版本统一回滚；
2. 确认相同 `R/hash(R)`；
3. 设置 `Htest`；
4. 全节点统一升级；
5. 开始 PoS V2/no-reorg/contract/DKVS 新一轮验收。

### 阶段 6：主网发布准备

1. 选择主网 `H`；
2. 固化 `H-1/hash(H-1)` checkpoint；
3. 固定 SatoshiNet/indexer/wallet SDK commit；
4. 验证 Bootstrap approval lock 持久化；
5. 确认所有生产节点升级；
6. 到达 `H` 后启用 PoS V2；
7. 合约/DKVS 激活高度不得早于 `H`。

---

## 12. 必须停止并重新讨论的条件

出现以下任一情况时，不得继续自动修改：

1. 新资产规则会导致现有主网区块、UTXO 或 spend journal 无法解码/验证；
2. PoS V2 需要修改历史区块 hash、txid 或历史签名；
3. EVM source verifier 无法取得准确 DeployTx pre-state 和 block context，却只能做弱 hash 比对；
4. 合约状态修复需要重写已有主网数据；
5. 测试网无法在 V1 下统一回滚到相同 hash；
6. 实现要求改变现有 SegWit v0 sighash；
7. 任何修改会覆盖当前未提交用户代码；
8. 需要提交、stage 或 push，但用户尚未明确授权。

---

## 13. 下一次 session 的直接执行入口

下一次继续时按以下顺序开始：

1. 读取本文档；
2. 读取仓库 `AGENTS.md`（如果存在）；
3. 检查 `git status` 和全部未提交修改；
4. 确认当前测试轮次已经结束；
5. 不覆盖当前 DKVS TTL changes；
6. 先实施“阶段 1”的最小非共识修复并增加测试；
7. 资产规则先只做扫描，不直接改变主网共识；
8. PoS V2 主网 activation 保持禁用；
9. 测试网 activation 高度在完成 V1 回滚后再设置；
10. EVM source 写入必须同时通过 exact init code 和 exact runtime replay，不能降级成仅检查 DeployTx 存在。
