# SatoshiNet 智能合约架构设计

本文定义 `satoshinet/contract` 的目标架构，用于指导 template、EVM、Agent 三类智能合约的统一重构。本文是 SatoshiNet 节点项目内部设计文档，关注代码边界、执行路径、状态存储和索引关系；对外协议语义仍以 `~/github/docs/circulation/contract` 与 `~/github/docs-en/circulation/contract` 为准。

## 设计目标

本次重构直接面向最终架构，不保留旧的 template 专属索引历史，不兼容旧的合约 index snapshot，也不把 indexer 作为合约状态机的一部分。

目标如下：

1. `contract` 顶层包成为外部模块可安全依赖的智能合约抽象层。
2. wallet SDK 等外部模块只依赖 `contract` 顶层包，不依赖 `contract/template`、`contract/evm`、`contract/agent`。
3. `contract` 顶层包不得 import EVM VM、go-ethereum 或任何具体合约 engine 的底层依赖。
4. template、EVM、Agent 三类合约统一使用同一套合约地址、交易 envelope、result tx、state root、index event 和 query model。
5. 合约状态执行只发生在 mining/blockchain/contract 层。
6. indexer 只消费已确认区块、canonical result tx、已提交 post-state 和统一 index event，不执行任何合约 runtime。
7. invoke 中能由 funding output 表达的经济参数，不在 OP_RETURN 中重复表达，避免双来源不一致。

## 包边界

目标目录结构：

```text
contract/
  types.go
  address.go
  script.go
  payload.go
  tx.go
  tx_builder.go
  result.go
  state.go
  index_event.go
  query.go

contract/engine/
  manager.go
  registry.go
  state_store.go

contract/template/
  engine.go
  runtime.go
  settlement.go
  ...

contract/evm/
  engine.go
  runtime.go
  ...

contract/agent/
  engine.go
  runtime.go
  ...
```

`contract` 顶层包只包含协议抽象、编码、地址、脚本、交易构造和查询模型。该包必须保持轻量，供 wallet SDK、市场、浏览器、测试工具等外部模块直接 import。

`contract/engine` 是节点执行侧的编排层。它可以依赖 `contract` 顶层抽象，但不应自动 import 所有 engine 实现。具体 engine 由 `server.go` 或节点启动装配代码显式注册：

```go
manager := contractengine.NewManager(
    template.NewEngine(...),
    evm.NewEngine(...),
    agent.NewEngine(...),
)
```

`contract/template`、`contract/evm`、`contract/agent` 只实现各自 engine 的业务语义和状态转换。外部模块默认不直接依赖这些包。

`contract` 顶层包已经接管原 `contract/common` 的协议类型、codec、script、gas、funding 和合约地址能力。外部模块 import `github.com/sat20-labs/satoshinet/contract` 不会拉入 EVM 底层依赖，也不需要再 import 某个具体 engine 包。

## 统一合约类型

顶层 `contract` 包定义统一类型：

```go
type ContractType byte

const (
    ContractTypeTemplate ContractType = 1
    ContractTypeEVM      ContractType = 2
    ContractTypeAgent    ContractType = 3
)

type TxKind byte

const (
    TxKindDeploy TxKind = iota + 1
    TxKindInvoke
    TxKindResult
    TxKindStateRoot
)

type ResultStatus byte
```

所有合约地址、OP_RETURN 内容类型、result payload、state root payload 都由顶层 `contract` 包定义。template/EVM/Agent 不再各自维护一套等价定义。

## 统一交易模型

所有合约交易解析后统一为 `contract.Tx`：

```go
type Tx struct {
    TxID         string
    Kind         TxKind
    ContractType ContractType
    Contract     string
    Subtype      string
    Version      uint32
    Action       string
    Actor        string
    GasLimit     uint64
    Nonce        uint64
    Payload      []byte
    Funding      []FundingOutput
}
```

`Contract` 由交易输出到合约地址的 funding output 解析得到。显式 invoke 不应在 OP_RETURN 中重复写入被调用合约地址。默认调用没有 OP_RETURN，按输出到合约地址的 funding output 触发。

`Actor` 由调用交易最后一个输入的前序输出地址解析得到。共识路径不使用 witness 公钥、签名公钥或其他可替换字段作为调用者身份来源。

## Invoke 参数归属

invoke 参数遵守单一事实来源原则：

1. 资产名称、资产数量、satoshi 数量、合约 funding 输入、gas/funding 输出等经济参数，以 funding output 为事实来源。
2. OP_RETURN 只携带调用 envelope、action、nonce、gas limit，以及无法由 funding output 表达的非经济参数。
3. 如果某个参数可以从 funding output、result input/output、合约地址或前序输出脚本确定，则不应在 OP_RETURN 中重复出现。
4. 如果业务需要非经济参数，例如滑点、最小输出、deadline 或证明 hash，则可以放在 OP_RETURN payload 中。
5. engine 执行时只消费抽象层解析后的 `FundingOutput` 和 `Payload`，不得自行建立第二套经济参数解析规则。

例如 AMM swap 中，用户输入的资产和数量来自 funding output；OP_RETURN 可以携带最小可接受输出、deadline 或交易方向等无法从 funding output 唯一确定的参数。若 OP_RETURN 和 funding output 对同一经济事实给出不同值，该交易设计本身就是错误的，协议层不应允许这种双来源结构存在。

EVM 合约也遵守同一规则。外部显式 invoke 的 OP_RETURN 只保存统一的 action/param，不直接保存 Solidity calldata；EVM 模块在内部根据 action、param 和 funding output 重建实际 calldata。

## Engine 接口

每类合约以 engine 形式接入统一执行层：

```go
type Engine interface {
    Type() contract.ContractType
    DecodeTx(tx *wire.MsgTx, ctx DecodeContext) (contract.Tx, bool, error)
    ValidateTx(tx contract.Tx, ctx ValidateContext) error
    ExecuteBlock(req EngineExecutionRequest) (EngineExecutionResult, error)
    VerifyResultTxs(req EngineResultVerifyRequest) error
    BuildStateView(state EngineState) ([]contract.StateView, error)
}
```

engine 只负责本类型合约的状态转换和业务语义。交易 envelope、合约地址、result tx 基本结构、state root commitment、index event 基本结构由统一层负责。

engine 执行上下文必须提供合约资产视图：

```go
type ContractAssetView interface {
    ContractAssets(contract string) (AssetSummary, error)
    ContractUTXOs(contract string, filters AssetFilters) ([]ContractUTXO, error)
}
```

该视图由 blockchain 当前 UTXO view 和当前区块 UTXO overlay 提供，不是外部 HTTP 服务，也不是只反映已确认区块的 indexer 查询结果。出块和验证必须使用同一套 UTXO view、资产解析规则和当前区块 overlay 规则。

## Contract Manager

`contract/engine.Manager` 是 mining 和 blockchain 的统一入口：

```go
type Manager interface {
    BuildBlockResults(req BlockBuildRequest) (BlockBuildResult, error)
    ValidateBlock(req BlockValidateRequest) (BlockValidateResult, error)
    BuildIndexEvents(req BlockIndexRequest) ([]contract.IndexEvent, error)
}
```

`BuildBlockResults` 用于出块，输入候选普通交易和 parent state，输出：

1. canonical result tx 列表。
2. block post-state。
3. combined contract state root。
4. index events。

`ValidateBlock` 用于验证已收到区块，必须独立重放所有相关 engine，校验：

1. 区块内合约交易顺序。
2. canonical result tx。
3. combined contract state root。
4. coinbase 中的 state root commitment。
5. gas 费用和 result tx 打包费用。

## 状态模型

统一状态集合：

```go
type StateSet struct {
    Engines map[contract.ContractType]EngineState
}

type EngineState interface {
    Root() [32]byte
    Clone() EngineState
    MarshalBinary() ([]byte, error)
}
```

`StateSet.CombinedRoot()` 是唯一计算合约 combined state root 的入口。template root、EVM root、Agent root 不应在 `server.go`、`blockchain` 或 indexer 中手工组合。

具体 engine 可以继续使用自己的内部状态结构和 codec，但必须通过 `EngineState` 暴露给统一层。

## 资产状态边界

合约执行同时看到两组资产数据：

1. 合约地址上的物理资产数据，由 blockchain UTXO view 加当前区块 overlay 统计得到。
2. 合约 runtime state 中的 managed assets，由合约业务规则登记，表示合约已经接受并承诺处置的资产。

这两组数据用途不同。物理资产数据回答“合约地址实际有什么”；managed assets 回答“合约状态机承认并准备按业务规则结算什么”。合约不能把 runtime state 中的 managed assets 当成合约地址真实余额，也不能只看物理余额来绕过业务状态约束。

有效调用进入合约后，合约应把需要管理的资产登记到 managed assets 中，包括业务资产和合约交互过程中积累的 gas 资产。无效调用最多扣除必要 gas 后退款；剩余 gas 可以作为合约管理 gas 积累，其他未被业务承认的资产不进入 managed assets。

生成 result tx 时，统一 result 层应同时使用这两组数据：

1. 先按 engine 输出的 intent/result plan 处置 managed assets。
2. result gas 从合约管理 gas 中扣除。
3. managed assets 在正常结算后如有剩余，按合约利润规则分配。
4. 合约地址物理资产中超出 managed assets 和 result gas 约束的部分，视为合约管理之外的资产，按统一策略转给 bootstrap 后续处理。

框架可以提供默认处理模板；具体 engine 仍可以在明确业务语义下决定如何使用物理资产数据和 managed assets。

合约地址上的 satoshi、ORDX、Runes、BRC20 或其他可由 UTXO 表达的资产余额，在共识执行路径中以 blockchain UTXO view 加当前区块 overlay 的统计结果为准。

indexer 的索引结果基于已经确认并连接的区块，适合作为提交后的查询视图，不适合作为候选区块构造或区块验证时的共识输入。候选区块中的 funding output、result tx 输出和同区块内花费关系尚未进入 confirmed indexer 结果，必须由 blockchain 的 UTXO view 和 block overlay 表达。

engine state 只保存业务状态和无法从 UTXO 集合直接推导的协议状态，例如：

1. 合约生命周期状态。
2. 订单、投注、触发器、执行进度等业务对象。
3. 已登记但尚未结算的 intent 或约束。
4. nonce、版本、权限、状态机阶段。
5. 需要参与 state root 的业务证明或摘要。

engine state 不应保存以下内容作为物理余额事实来源：

1. 合约地址当前资产总余额。
2. 合约地址当前 satoshi 总余额。
3. 可由合约 UTXO 集合统计得到的资产池余额。
4. 可由 committed Result TX 和 UTXO 变更推导得到的资产进出账。

如果某类合约需要判断资金池是否 ready、库存是否足够、gas 是否足够或 `address(this).balance`，必须通过 `ContractAssetView` 读取合约地址资产统计。若 engine 内部为了计算方便维护缓存、承诺余额或结算池，该状态只能表达 managed assets 或业务约束，不得替代 UTXO view 的物理余额事实来源。

这一边界的原因是：资产归属已经由 SatoshiNet UTXO 模型确定，合约 runtime state 再保存一份余额会形成双来源，容易在 Result TX、reorg、索引恢复或跨 engine 查询时产生不一致。indexer 应消费提交后的 state/events/UTXO 结果来服务查询，而不是反向参与合约共识执行。

## 状态存储

状态存储由统一接口管理：

```go
type StateStore interface {
    LoadParentState(block *btcutil.Block) (*StateSet, error)
    StoreBlockState(hash *chainhash.Hash, state *StateSet, events []contract.IndexEvent) error
    LoadBlockState(hash *chainhash.Hash) (*StateSet, error)
    LoadBlockEvents(hash *chainhash.Hash) ([]contract.IndexEvent, error)
    DeleteBlockState(hash *chainhash.Hash, newTip *chainhash.Hash) error
}
```

数据库 bucket 可以按 engine namespace 保存具体状态，但外部只通过统一 state store 读写：

```text
contractstate/
  tip
  byblock/<blockHash>/state
  byblock/<blockHash>/events
  engines/template/...
  engines/evm/...
  engines/agent/...
```

connect block 时，blockchain 在同一个提交边界内写入 block post-state 和 index events。disconnect/reorg 时，按 block hash 删除状态和事件，并把 tip 回退到新主链 tip。

## 统一 Index Event

合约历史使用统一 `contract.IndexEvent`：

```go
type IndexEvent struct {
    Kind         string
    Height       int64
    TxID         string
    Contract     string
    ContractType ContractType
    Subtype      string
    Action       string
    Status       string
    Actor        string
    GasLimit     uint64
    Nonce        uint64
    Details      map[string]any
}
```

不再保留 template 专属 `HistoryRecord` 作为 indexer 历史模型。template 的订单 item、AMM settlement、exchange settlement 等业务细节如需展示，放入 `Details`，但主结构必须统一。

Index event 由执行层生成并随 block post-state 提交。indexer 不通过重放 runtime 生成 history。

## Indexer 边界

indexer 只依赖统一索引源：

```go
type ContractIndexSource interface {
    BlockEvents(hash *chainhash.Hash) ([]contract.IndexEvent, error)
    BlockStateView(hash *chainhash.Hash) ([]contract.StateView, error)
}
```

indexer 连接区块时：

1. 读取已提交的 block events。
2. 读取必要的 block state view。
3. 将合约摘要、history、辅助查询索引写入 contract 子索引模块。
4. contract 子索引模块跟随 `IndexerMgr` 的 `prepareDBBuffer -> performUpdateDBInBuffer -> cleanDBBuffer` 生命周期：`Clone` 保存待提交增量，backup 写 DB 前必须先对 latest 调用 `Subtract`，从最新内存中裁剪已落库的旧增量；随后 `UpdateDB` 将 backup 中的 syncHeight 视图落库。
5. RPC/HTTP query 从 DB 中读取 syncHeight 之前的数据，并叠加内存中尚未落库的最新增量，提供稳定视图。

indexer 必须删除以下边界：

1. `templateRuntimeStore`
2. `templateContractIndex`
3. `templateContractHistory`
4. `templateContractIndexSnapshotKey`
5. `TemplateContractQueryStore`
6. indexer 内对 `template.ExecuteBlock`、`evm.ExecuteBlock`、`agent.ExecuteBlock` 的任何调用

IndexerMgr 不直接管理 contract 索引 map，也不维护 contract snapshot blob；它只持有 contract 子索引实例及其 backup，并像 L1 indexer 的 BRC20/FT 子模块一样调用 `Clone`、`UpdateDB`、`Subtract`。

保留统一查询：

1. `GetContractSummaries`
2. `GetContractSummary`
3. `GetContractHistory`
4. 基于 `StateView` 的统一 contract state/query 接口

## 查询模型

顶层 `contract` 包定义统一 query model：

```go
type ContractSummary struct {
    Address        string
    ContractType   ContractType
    Subtype        string
    Name           string
    Version        uint32
    Status         string
    CreatedHeight  int64
    UpdatedHeight  int64
    Details        map[string]any
}

type StateView struct {
    Contract      string
    ContractType  ContractType
    Subtype       string
    StateRoot     [32]byte
    Assets        AssetSummary
    UpdatedHeight int64
    Status        string
    Details       map[string]any
}
```

`StateView.Assets` 来自已提交区块后的合约地址 UTXO 统计视图，不来自 engine state。template/EVM/Agent 的差异通过 `Subtype`、`Action`、`Status`、`Details` 和 `StateView` 表达。外部 query 不应要求调用方直接理解某个 engine 的 runtime store。

## 执行流程

出块流程：

1. mining 选择普通交易。
2. `contract/engine.Manager` 解析并排序合约交易。
3. manager 加载 parent `StateSet`。
4. manager 按确定顺序调用相关 engine。
5. engine 返回本类型 result tx、post-state 和 index events。
6. manager 汇总 result tx，计算 combined state root。
7. mining 把 result tx 加入候选区块，并把 combined state root 写入 coinbase。

验证流程：

1. blockchain 加载 parent `StateSet`。
2. manager 解析区块内合约交易和 result tx。
3. manager 按协议顺序重放所有 engine。
4. manager 校验区块 result tx 与本地 canonical result tx 一致。
5. manager 计算 combined state root。
6. blockchain 校验 coinbase state root commitment。
7. 验证通过后，connect block 提交 post-state 和 index events。

索引流程：

1. indexer 收到已连接区块通知。
2. indexer 读取该 block hash 对应的 committed events。
3. indexer 更新 summary/history。
4. indexer 读取 state view 更新状态查询视图。
5. indexer 不执行 runtime，不计算 state root，不生成 result tx。

## Result TX

canonical result tx 的公共规则由顶层 `contract` 包定义。engine 只输出资产转移 intent、状态变化摘要和错误状态。

统一层负责：

1. result payload 编码。
2. result tx 输入选择。
3. result tx 输出排序。
4. result tx OP_RETURN 位置。
5. result tx 与执行项绑定。
6. result tx 验证。

具体 engine 不应各自复制一套 result tx builder 和 verifier。若某个 engine 有特殊资产结算规则，应表达为 intent 或 result plan，再交给统一 result 层构造交易。

## Gas

gas 参数由统一层管理。`GasConfig` 应只包含数字参数和协议规则，不携带网络特定资产名称。gas 资产名称由网络参数在装配层解析一次，并通过执行上下文传入。

所有 engine 使用同一 gas 资产和统一费用归集规则。具体 VM 的 gas 消耗可以由 engine 内部计算，但 gas 费用扣除、miner 归属和 result 打包费用归统一层处理。

## 删除清单

最终重构完成后，应删除或合并以下重复概念：

1. 原 `contract/common` 中的公共类型和 codec 已上移到 `contract` 顶层，公开 `contract/common` 包应保持删除状态。
2. template/EVM/Agent 各自重复的 `TxType`、`ResultPayload`、`StateRootPayload`。
3. 三套外部 block validator 外观。
4. 三套外部 result builder 外观。
5. 三套 state store 外观。
6. indexer 内 template runtime 执行和 template 专属 snapshot。
7. query 层 template 专属 store 接口。

内部 runtime、状态结构和业务算法可以保留在各 engine 包内，但必须通过统一接口接入。

## 测试要求

重构完成后至少验证：

1. wallet SDK import `contract` 不会拉入 EVM 底层依赖。
2. template、EVM、Agent deploy/invoke/result 都走统一 manager。
3. 同一区块混合 template/EVM/Agent 时 result tx 顺序确定。
4. combined state root 只由 `StateSet.CombinedRoot()` 计算。
5. 合约共识执行中的资产余额来自 blockchain UTXO view 和当前区块 overlay，不保存在 engine state 中作为事实来源。
6. blockchain 重放验证得到与 mining 相同的 result tx 和 state root。
7. connect block 后 state/events 一次提交。
8. reorg 后 state/events/indexer 视图正确回退。
9. indexer history 完全来自 committed index events。
10. contract query 返回的数据能追溯到 confirmed block、result tx、post-state、asset index 和 index event。
11. invoke 经济参数只来自 funding output，不存在 OP_RETURN 与 funding output 双来源冲突。

建议测试命令：

```bash
go test ./contract/...
go test ./blockchain/...
go test ./indexer/...
go test ./integration/evme2e -run 'Template|Solidity|Agent'
```
