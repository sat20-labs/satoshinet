# SatoshiNet btcd 差异功能审查整改与 PoS V2 实施计划

更新时间：2026-10-06

适用仓库：`sat20-labs/satoshinet`，以及需要联动的 `sat20-labs/indexer`、`sat20-labs/sat20wallet`  
文档状态：**已完成本机源码静态盘点；PoS V2 与 EVM source 等仍待实施，阶段 1 的直接修复已落地**
文档用途：供下一次开发 session 直接读取并继续实施；与此前聊天中的阶段性总结冲突时，以本文为准。

2026-10-05 补充确认：替补出块的奖励地址规则与 PoS V2 使用同一激活高度，保留激活前历史验证规则，后续统一实施。本次只更新计划，不实施代码或部署。

状态口径（2026-10-06 本机源码静态核对）：

- **已实现**：当前源码已包含该项实现；不代表本次运行过测试或完成远程节点验收。
- **部分实现**：已有相关基础能力，但仍未满足本节列出的全部规则。
- **未实现**：当前源码未找到该项所需实现。
- **已替代**：原方案已由另一份规范文档取代，应按新文档继续维护和验收。
- **待确认**：需要测试日志、运行状态或其他本机源码之外的证据才能判断。

本次只检查源码和已有测试文件，没有运行测试，也没有检查 101/102/103 上的运行版本或部署状态。

---

## 1. 执行约束

1. 本轮状态复核只更新本文档，不修改生产代码、不替换节点二进制、不重启测试网络；后续实施须先确认当前测试轮次完整结束。
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

> 状态：**部分实现** — 当前保留 PoS V1 的生产者/替补和费用模板能力；PoS V2 激活、证书、no-reorg 等共识规则尚未实现。

PoS V2 采用未来激活高度，整体启用以下规则：

- 完整区块 proposal digest；
- producer signature；
- Bootstrap finality certificate；
- Miner → Core → Bootstrap 替补出块；
- 奖励地址与实际出块者分开验证，按第 3.4.1 节确定费用接收通道；
- Bootstrap 一高度一签名的持久化锁；
- direct-tip only；
- 禁止 side chain、PoS V2 orphan 延伸和 automatic reorg；
- 不再使用累计 PoW work 做 fork choice；
- PoS V2 固定规范 Bits；
- 零 BTC 区块补贴；
- `BFFastAdd` 不能跳过 PoS V2 校验。

### 2.2 No-reorg 目标

> 状态：**未实现** — 区块链仍按累计 chainwork 选择主链，并保留侧链和 reorg 路径。

PoS V2 激活后，聪网把已接入的区块视为最终确认区块。网络分区时，没有对应 Bootstrap finalizer 的一侧应停止出块，而不是生成可在恢复后参与链重组的侧链。

### 2.3 EVM source metadata

> 状态：**未实现** — 当前 source metadata 仍由 indexer 的 contract indexer/API 管理；尚未迁入经确定性编译和链上 bytecode 验证的 DKVS 系统 Blob。

EVM 合约源码不保存在 contract indexer。规范存储位置为：

```text
/blob/evm/source/<contract_address>
```

其中 `<contract_address>` 是 DeployTx 成功后生成的、规范编码的 SatoshiNet EVM 合约地址。路径中的地址直接用于查找，不再做额外 hash 映射。

DeployTx 已支付的 deploy fee 同时包含该合约一个永久源码 Blob 的一次性存储费用，不再收取 DKVS AUTOPAY、LEASE 或独立存储费。

任何人都可以提交源码，但**节点只有在确定性编译和链上 bytecode 验证全部通过后，才允许源码进入该规范路径**。该路径中存在的源码即表示“已验证为该合约的源码表示”，不得保存未经验证的候选源码。

### 2.4 明确不修改

> 状态：**设计约束** — 这些是实施边界，不是独立代码重构任务。

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

> 状态：**未实现** — `chaincfg.Params` 中没有 `PosV2ActivationHeight`，主网/测试网也没有本计划要求的 PoS V2 高度开关。

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

第 3.4.1 节的奖励地址规则也从 `H` 开始执行，不设置独立奖励激活高度。`height < H` 保留原 coinbase 格式、签名及奖励地址验证，不回溯要求历史替补区块支付到新通道。应用版本号（例如 `1.0.0`）不能代替共识激活高度。

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

> 状态：**未实现** — 当前 PoS 签名尚未绑定规范化的完整区块 proposal digest。

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

> 状态：**未实现** — 尚无 coinbase 中的 PoS V2 producer/finalizer 双签名结构和对应验证。

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

> 状态：**部分实现** — 当前有 PoS V1 的生产者选择和替补链路；PoS V2 对 expected/actual producer、替补层级及证书身份的共识验证未实现。

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

### 3.4.1 替补出块的奖励地址（2026-10-05 确认）

> 状态：**未实现** — 仍需按原定时隙和实际替补者推导接收通道，并将同一规则用于模板、区块验证和 replay。

不改变 Miner、Core、Bootstrap 的出块时隙和替补顺序。仅在 PoS V2 激活后，按原定时隙和实际出块者确定 coinbase 的经济输出地址：

| 原定时隙 | 实际出块者 | 奖励地址 |
| --- | --- | --- |
| Miner | 原定 Miner | 原定 Miner 与其所属 Core 的通道地址 |
| Miner | 所属 Core 替补 | 同一个 Miner–Core 通道地址，与 Miner 是否在线无关 |
| Miner | 所属 Bootstrap 替补 | 该 Miner 所属 Core 与 Bootstrap 的通道地址 |
| Core | 原定 Core | 原定 Core 与其所属 Bootstrap 的通道地址 |
| Core | 所属 Bootstrap 替补 | 同一个 Core–Bootstrap 通道地址 |
| Bootstrap | 原定 Bootstrap | 保持该 Bootstrap 自身的奖励地址 |

Miner 和 Core 失联或未按时出块时，Bootstrap 按现有替补策略接管，收益进入该组 Core–Bootstrap 通道。这里的“都不在”是出块协调条件，不是 replay 时需要证明的历史在线状态。普通验证节点不查询当前 peer 连接状态，不根据当前墙钟推断历史节点是否离线。

本规则适用于 coinbase 归集的 BTC fees 和资产 fee/gas；不增加区块补贴，也不改变费用金额计算或通道内部收益分配规则。Core 自己的时隙仍奖励 Core–Bootstrap 通道，不得任意选择一个子 Miner 作为接收者。

实现必须分别确定和验证：

- `expectedProducer`：从 candidate 的 direct-parent 排序机状态确定原定时隙；
- `actualProducer`：验证真实签名者属于该时隙允许的替补层级；
- 奖励地址：用该时隙的节点公钥关系和实际出块者层级，按上表推导。

复用已有排序机和通道地址推导能力，不新增奖励地址持久字段、离线证明或独立补偿机制。不能再根据奖励地址反查节点公钥并将其视为实际出块者。例如 Core 替补 Miner 时，奖励地址归属 Miner–Core 通道，但 producer signature 必须由 Core 公钥验证。

出块模板必须在执行合约、归集费用和生成 proposal digest 之前选定奖励地址。完整 proposal digest 绑定该经济输出；Bootstrap 审核、普通 block 验证、`BFFastAdd` 路径均执行同一奖励规则。在线 proposal 的签名校验、替补超时判断、审核转发及下一时隙通知，也必须使用实际出块者和原定时隙各自的身份，不从收款地址推断身份。

历史 replay 按候选区块高度选择规则：`height < H` 接受原规则下合法的 Core/Bootstrap 替补区块及原奖励地址；`height >= H` 严格执行新证书和上表地址规则。不得修改历史 coinbase、txid、block hash、合约 Result 或 state root。排序机继续按原定时隙推进和恢复，不根据替补收款地址改变顺序；无需转换历史数据库。

## 3.5 Bootstrap finality certificate

> 状态：**部分实现** — `MsgMineBlock`/`MsgMineAck` 审核消息链路存在，但 Bootstrap 对 proposal digest 的 finality 签名和区块内证书校验尚未实现。

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
4. Bootstrap 验证 direct-tip、排序、替补层级、第 3.4.1 节奖励地址、交易、资产、Anchor、合约和 state root；
5. Bootstrap 签同一个 `PosProposalDigestV1`；
6. `MsgMineAck` 返回 `FinalizerSignature`；
7. producer 将两个签名写入最终 coinbase，重新计算 Merkle root；
8. 最终区块通过普通 block P2P 路径广播。

所有节点根据链上排序机状态确定 producer/finalizer 公钥，不把 `peer.ValidatorId` 当作共识身份依据。

## 3.6 Bootstrap 一高度一签名锁

> 状态：**未实现** — 未找到先持久化 approval lock、重启恢复并拒绝同高度不同 digest 的实现。

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

> 状态：**未实现** — 当前仍允许非 tip 父块进入侧链并参与后续主链选择。

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

> 状态：**未实现** — `connectBestChain` 仍比较累计 chainwork 并可触发 reorg。

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

> 状态：**部分实现** — `BFFastAdd` 前仍会执行部分现有区块顺序和挖矿信息检查；PoS V2 证书、替补身份和奖励地址校验尚不存在，KnownValid 快速路径也仍会跳过部分完整验证。

以下检查必须始终执行：

- PoS V2 coinbase/certificate 格式；
- producer signature；
- finalizer signature；
- expected producer；
- actual producer 替补层级；
- 与原定时隙及实际出块者层级对应的奖励地址；
- finalizer 身份；
- direct-tip；
- 固定 Bits；
- 零补贴；
- combined state root。

## 3.10 零 BTC 补贴

> 状态：**部分实现** — 当前挖矿模板以零 BTC 补贴起步、只加交易费；共识验证仍允许 `CalcBlockSubsidy(height) + fees`，尚未按 PoS V2 激活高度收紧上限。

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

> 状态：**部分实现** — 合约/AIDX 有 candidate parent readiness 检查；它不是 PoS V2 独立的排序机 tip/hash 门禁，且仍保留分支视图路径。

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

> 状态：**未实现** — 当前没有 PoS V2 激活参数，因而也没有强制 `ContractActivationHeight >= PosV2ActivationHeight` 的检查。

主网要求：

```text
ContractActivationHeight >= PosV2ActivationHeight
```

避免出现智能合约已经产生状态，但链仍允许常规 reorg 的生产阶段。

---

## 4. 资产共识安全

以下修改前必须先扫描主网历史数据。任何一项发现历史不兼容记录时，停止修改并重新讨论未来激活规则。

## 4.1 禁止负数和零数量

> 状态：**部分实现** — 共识输出检查会拒绝负数和非法 Decimal，但仍接受零数量；正数量规则和相关输入、coinbase、合约输出边界尚未完整统一。

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

> 状态：**部分实现** — 已检查普通交易输入/输出 BindingSat 一致性、冲突输入和 carrier sats；与 ticker 注册信息的一致性及历史数据扫描报告仍未完成。

要求：

- 同一 AssetName 的 `BindingSat` 不得在转账中任意改变；
- 与 ticker 注册信息一致；
- 同一输出中同名资产不能出现不同 `BindingSat`；
- 输出 sats 必须满足绑定资产所需聪数：

```text
TxOut.Value >= required_binding_sats
```

## 4.3 资产列表 canonical form

> 状态：**部分实现** — 普通交易输出已有排序和重复项检查；空字段、所有入口的一致校验及完整 canonical 编码约束尚未覆盖。

每个 `TxOut.Assets` 必须：

- AssetName 唯一；
- 严格排序；
- 无重复；
- 无空字段；
- Decimal 合法；
- BindingSat 不溢出；
- 具有唯一 canonical serialization。

## 4.4 AssetName 严格三段

> 状态：**部分实现** — 字符串构造器要求三段，但不保证每段非空，二进制资产字段和其他入口也尚未统一执行完整规则。

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

> 状态：**未实现** — 当前有交易/协议总大小限制，但未找到本节列出的资产项数、字段长度、单输出资产编码和 BindingSat 专项上限。

增加明确上限：

- 每个输出最大资产项数；
- Protocol 最大长度；
- Type 最大长度；
- Ticker 最大长度；
- Decimal 文本最大长度；
- 单输出资产总编码大小；
- BindingSat 范围。

## 4.6 SegWit v0 sighash

> 状态：**部分实现** — 当前 SegWit v0 sighash 已纳入 prevout 资产数据且算法保持不变；本节要求的专用固定向量和 SDK 可信 UTXO 来源尚未完整确认。

保持当前算法，不修改历史签名摘要。

仅增加：

- 固定 sighash vector；
- wallet SDK 从可信 UTXO 数据获取 prevout assets；
- 禁止未来无意改变已有算法。

---

## 5. 智能合约状态

## 5.1 每个激活后区块都承诺 combined state root

> 状态：**部分实现** — combined root、coinbase commitment 和多个模块的状态校验已存在；当前没有合约 activity 的区块可直接跳过验证，因此尚未满足“激活后每块都承诺”的规则。

合约激活以后，无论区块是否有合约 activity，都必须有且只有一个 combined state root commitment。

计算：

- 有模块 activity：使用模块 post-state root；
- 无模块 activity：使用模块 parent-state root；
- 模块从未产生状态：使用零 root；
- 最后组合 Template、EVM、Agent root。

无 activity 区块承诺父状态不变后的 combined root。缺失、重复或错误 commitment 均拒绝。

## 5.2 状态大小在 validation 阶段检查

> 状态：**未实现** — 16 MiB 检查目前用于状态持久化边界，没有在 block validation 阶段对各模块 post-state 执行同一限制。

当前 16 MiB 限制不能只在数据库持久化时执行。Template、EVM、Agent 的 post-state 必须在 block validation 阶段执行完全相同的确定性大小检查，避免“共识验证通过、DB 提交失败”。

## 5.3 KnownValid 与 post-state

> 状态：**部分实现** — transient post-state 的缓存、提交和回收能力已存在；未找到 KnownValid 区块缺少缓存状态时强制重执行合约的完整路径。

如果 block 被标记 KnownValid，但 transient post-state 已丢失：

- 有 post-state：提交；
- 没有 post-state：重新执行合约；
- 不能静默跳过状态写入。

## 5.4 状态快照清理

> 状态：**部分实现** — 已有 transient post-state 回收和状态存储清理钩子；最近诊断窗口、finalized checkpoint 与生产/测试网差异化保留策略尚未形成完整方案。

需要设计：

- 最近测试/诊断窗口；
- 当前 tip state；
- finalized checkpoint；
- 旧 block snapshot pruning。

PoS V2 生产环境不允许 reorg，可采用较小历史快照窗口；测试网可保留更长诊断窗口。

---

## 6. EVM source metadata：规范系统 Blob

## 6.1 核心保证

> 状态：**未实现** — 当前源码没有“保存即表示源码与链上 init/runtime bytecode 精确等价”的保证。

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

> 状态：**未实现** — 尚无 `/blob/evm/source/<contract_address>` 规范 key 和对应 canonical address 校验。

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

> 状态：**未实现** — 尚无面向 EVM source 的 verified public write-once DKVS 专用写入模式。

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

> 状态：**未实现** — 当前 source metadata API/schema 不是本节定义的确定性、版本化 source package。

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

> 状态：**未实现** — 找到的 `solc` 调用用于 E2E/开发编译，没有节点侧固定编译器白名单、sandbox 和 fail-closed 写入流程。

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

> 状态：**未实现** — 尚无从 DeployTx 查询、编译、精确 init code 比对、准确上下文 runtime replay 到原子写入的节点流程。

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

> 状态：**未实现** — DeployTx source entitlement 与 DKVS 一次性永久 Blob 槽位尚未实现。

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

> 状态：**未实现** — 尚无基于 DeployTx entitlement 和 bytecode 验证、而非 deployer 身份的 source 写入授权。

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

> 状态：**未实现** — source metadata 仍保存在 contract indexer；API 尚未改为读写规范 DKVS 系统 Blob。

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

> 状态：**未实现** — 当前没有覆盖本节验证、write-once、runtime replay 等要求的节点侧功能实现；现有 Solidity E2E 编译测试不等同于这些验收。

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

## 7. DKVS 当前状态同步安全

> 状态：**已替代** — 原方案已由 [`dkvs-design.md`](./dkvs-design.md) 取代；当前源码可见 snapshot 来源认证、签名和安装 baseline 检查。后续按新设计文档验收，本节不再作为实现清单。

> 本节原始方案已被 2026-10-04 的最终 DKVS 设计替代。规范以
> [dkvs-design.md](./dkvs-design.md) 为准，本节只保留 review 结论。

当前约束：

- P2P 不传播 generation；
- 一次 current-set sync session 固定同一个已认证 Core/Bootstrap source；
- P2P snapshot 只包含当前 records、root、view height 和分页 cursor；
- 不包含 tombstone、delete floor、mutation history 或永久 source state；
- 节点同步完成前把 Notify 缓存在内存，完成后再按到达顺序处理；
- Notify 只要求来源是有效 Core/Bootstrap，不要求等于上一次同步 source；
- destructive current-set install 必须验证 session/source signature、scope/root 和本地安装 baseline；
- FREE_LOCAL 不进入 P2P。

普通 Data/Inv 内容不能因为“曾经被请求且签名有效”就恢复已删除值；无法确认 current state 时转 current-set sync。

---

## 8. 内嵌 indexer 与网络判断

## 8.1 Stake/unstake panic

> 状态：**已实现** — stake/unstake 解析错误和缺失资产信息会提前返回；对应回归测试文件已存在，未在本次运行。

修复：

- `handleStakeAssetV2` 中反向的 `channelMap` 判断；
- `Assets.Find()` 失败后解引用 nil；
- `removeMinerNode` 同类 nil dereference；
- 合法或恶意 OP_RETURN 不得让节点永久 panic 在某高度。

## 8.2 BaseIndexer Clone

> 状态：**已实现** — `Clone` 已深拷贝相关 map、UTXO/index 和可变记录；对应 Clone/DB snapshot 回归测试已存在，未在本次运行。

对以下可变对象执行真正深拷贝：

- `tickInfoMap`；
- `channelMap`；
- `utxoIndex.Index`；
- ascend/descend/ledger/event/referrer maps；
- 其他会在 live compiling 中继续修改的指针对象。

`Clone(true)` 不得在持有 `RLock` 时修改 live `AddressValueV2.Op`。

## 8.3 主网判断

> 状态：**已实现** — `IsMainnet()` 已按 `chaincfg.Params.Net == wire.MainNet` 判断并处理 nil；已有测试文件，未在本次运行。

最小修复：

```go
func (b *IndexerMgr) IsMainnet() bool {
    return b.chaincfgParam != nil &&
        b.chaincfgParam.Net == wire.MainNet
}
```

本轮不统一其他组件的 network name。

## 8.4 Channel state event 并发保护

> 状态：**已实现（范围内）** — `RecordChannelStateEvent` 已加互斥锁并保存独立副本；当前实现仍在 Flush 期间持有全局锁，写入失败时内存 map 不回滚，源码注释已记录这两项限制。

`RecordChannelStateEvent` 修改共享 map 时增加正确 mutex。生产禁用规则依赖修复后的 `IsMainnet()`。

---

## 9. 挖矿模板与提交

## 9.1 `SubmitNewBlock`

> 状态：**已实现** — 提交回调返回 false 时现在返回错误；对应测试文件已存在，未在本次运行。

当前不能忽略 `submitBlock()` 的 bool 结果。修改后：

- stale：返回 error；
- orphan：返回 error；
- rule rejection：返回 error；
- 只有区块真正接入主链才返回 hash/height success。

## 9.2 Anchor/DeAnchor 模板资源

> 状态：**已实现** — Anchor/DeAnchor 已进入模板选择流程，并执行区块重量、sigops、输入/UTXO、重复 Anchor funding、费用及依赖排序检查；相关测试文件已存在，未在本次运行。

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

> 状态：**本次未运行测试** — 以下是验收要求，不代表通过；每个分组的状态只描述已有代码覆盖情况。

## 10.1 PoS V2

> 状态：**未实现/未验收** — PoS V2 核心共识实现尚未落地，本组要求没有可运行的完整验收对象。

至少覆盖：

- 激活前 V1 区块有效；
- 激活高度无 certificate 拒绝；
- 正确 producer/finalizer 接受；
- Miner、Core、Bootstrap 替补；
- 第 3.4.1 节全部时隙/实际出块者组合的奖励地址；
- Core 替补 Miner：Core producer signature 配合 Miner–Core 收款地址接受，用 Miner 公钥验证该 Core 签名不得通过；
- Miner 时隙中，Core 替补却支付到 Core–Bootstrap 通道，在 `height >= H` 拒绝；对应历史合法区块在 `height < H` 仍接受；
- Bootstrap 替补 Miner/Core 时支付对应 Core–Bootstrap 通道，不能支付 Bootstrap 自身地址或其他组通道；Bootstrap 自己时隙的地址保持有效；
- 完整 proposal 签名后改动奖励地址，producer/finalizer 签名验证失败；
- 激活前真实历史区块 replay（含 Core/Bootstrap 替补），不改历史 txid、block hash、合约 Result 和 state root；
- `H-1`/`H` 边界同步、重启恢复和排序机推进一致；replay 的地址验证不依赖当前 peer 状态或墙钟；
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

> 状态：**部分实现/未验收** — 已有输出 canonical 顺序、BindingSat 和负资产相关测试；零值、严格 AssetName、资源上限及历史扫描验收仍缺。

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

> 状态：**部分实现/未验收** — 已有 state root、模块状态和 post-state 相关测试；validation 大小限制、KnownValid 恢复和快照保留策略仍缺。

至少覆盖：

- 无 activity 区块的父 combined root；
- 缺失/重复/错误 state root；
- validation 阶段状态过大；
- KnownValid post-state 丢失后重执行；
- state snapshot pruning；
- mixed Template/EVM/Agent；
- 更新不可编译的 legacy EVM E2E。

## 10.4 DKVS

> 状态：**已替代/未验收** — 应使用最终 `dkvs-design.md` 及对应 SDK/节点验收记录；本次未运行这些测试。

至少覆盖：

- 未授权 snapshot source 拒绝；
- 授权 Core/Bootstrap 接受；
- 自声明 ValidatorId 但不在 authority 集合中拒绝；
- sync session source 验证与 realtime Notify source 验证分离；
- EVM source 特殊 path、fee、compile、runtime replay 和 write-once 测试。

## 10.5 长测试基础设施

> 状态：**待确认** — 本次未检查测试 supervisor 的运行记录，也未运行长测试以验证终态与日志收集行为。

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

> 状态：**待确认** — 聊天记录中的主网验收已通过，但当前测试轮次是否结束、commit/tip/log 基线是否归档，不能仅凭本机源码确认。

1. 冻结代码；
2. 完成本轮测试；
3. 保存日志、commit、tip 和 DB 备份；
4. 修复测试 supervisor 终态记录问题。

### 阶段 1：无历史格式影响的直接修复

> 状态：**已实现** — 本阶段列出的 IsMainnet、channel event mutex、stake/unstake、Clone、SubmitNewBlock、Anchor/DeAnchor，以及 DKVS TTL 注释和 snapshot authority 修复均可在当前源码中找到；本次未运行测试。

1. `IsMainnet()`；
2. channel state event mutex；
3. stake/unstake panic；
4. BaseIndexer Clone；
5. `SubmitNewBlock` 返回值；
6. Anchor/DeAnchor 模板资源；
7. DKVS TTL 注释；
8. DKVS snapshot authority。

### 阶段 2：资产历史扫描与安全规则

> 状态：**部分实现** — 若干基础共识校验已落地；主网兼容扫描工具/报告和完整规则仍未完成，不能据此收紧剩余共识规则。

1. 编写主网数据扫描工具；
2. 输出资产规范兼容报告；
3. 若全部兼容，再落地负数、零值、重复、排序、BindingSat、严格三段和资源上限；
4. 若发现不兼容，停止并重新设计未来激活规则。

### 阶段 3：PoS V2

> 状态：**未实现** — 本阶段共识规则在当前源码中尚未落地；主网 activation 保持未启用。

1. 增加 activation params；
2. proposal digest；
3. producer/finalizer certificate；
4. approval lock；
5. direct-tip/no-reorg；
6. 停用 chainwork fork choice；
7. 固定 Bits；
8. 零补贴；
9. direct-parent indexer readiness；
10. 第 3.4.1 节奖励地址推导、模板支付和实际出块者验证分离；
11. 激活前历史 replay、激活边界及多节点 E2E。

主网高度保持未激活。

### 阶段 4：合约状态与 EVM source

> 状态：**部分实现** — combined state root、状态存储和 transient post-state 框架已存在；本阶段未完成的 validation/恢复/清理改造和全部 EVM source 系统 Blob 功能仍待实施。

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

> 状态：**待执行** — 需要先确认回滚高度/hash、统一版本和激活参数；当前源码没有 PoS V2 activation 参数。

1. 使用 V1 版本统一回滚；
2. 确认相同 `R/hash(R)`；
3. 设置 `Htest`；
4. 全节点统一升级；
5. 开始 PoS V2/no-reorg/contract/DKVS 新一轮验收。

### 阶段 6：主网发布准备

> 状态：**未开始** — PoS V2 activation、finalizer lock 和相关发布 checkpoint 尚不存在。

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

> 状态：**已更新** — 阶段 1 已不再是待实施任务；继续工作前应先确认当前测试基线，再从资产历史扫描和未实现的 PoS V2/合约项开始。

下一次继续时按以下顺序开始：

1. 读取本文档；
2. 读取仓库 `AGENTS.md`（如果存在）；
3. 检查 `git status` 和全部未提交修改；
4. 确认当前测试轮次已经结束；
5. 阶段 1 的直接修复已在当前源码中，不要重复实施；
6. 资产规则先完成主网历史扫描和兼容报告，不直接改变主网共识；
7. PoS V2 主网 activation 保持禁用，测试网 activation 高度只能在 V1 回滚并统一 tip 后设置；
8. 合约状态项先补齐 validation、KnownValid 和状态保留缺口；
9. EVM source 写入必须同时通过 exact init code 和 exact runtime replay，不能降级成仅检查 DeployTx 存在；
10. 不 stage、commit 或 push，除非用户另行明确要求。
