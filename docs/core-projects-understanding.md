# Core Projects Understanding

> 记录时间：2026-04-26。本文是 Codex 对当前 workspace 中核心项目的基础理解，供后续协作时作为默认上下文。当前只关注 `indexer`、`docs`、`docs-en`、`sat20wallet`、`transcend`、`satoshinet`；其他仓库本轮不纳入基础记忆范围。

## 总体关系

这组项目围绕 SAT20 协议栈展开：

```text
docs / docs-en
  -> 描述 SAT20、ORDX、STP、SatoshiNet 的产品和协议语义

indexer
  -> BTC L1 资产索引器，索引 UTXO、sat range、Ordinals、ORDX、BRC-20、Runes 等

satoshinet
  -> 基于 btcd 的 SatoshiNet 节点和 L2 环境，内置/关联 SatoshiNet 侧索引能力

sat20wallet/sdk
  -> 钱包和客户端 SDK，统一访问 L1 indexer、L2 SatoshiNet/indexer，并构造/签名交易

transcend
  -> STP 服务，基于 wallet SDK、L1 indexer、L2 SatoshiNet，管理跨层通道、锁定、解锁、升降层等流程
```

`docs` 和 `docs-en` 解释“为什么”和“协议应该是什么”；`indexer`、`satoshinet`、`sat20wallet/sdk`、`transcend` 分别承担“L1 索引”“L2 网络”“钱包客户端抽象”“跨层通道协议服务”。

## indexer

路径：`~/github/indexer`

模块：`github.com/sat20-labs/indexer`

定位：BTC 主网/testnet/testnet4 的 SAT20/ORDX 资产索引器。它从 bitcoind 拉取区块，维护基础 UTXO、地址、sat ordinal range 状态，并在此基础上索引 Ordinals 铭文、ORDX FT/NS/NFT、BRC-20、Runes、稀有聪等数据，通过 Gin HTTP API 对外查询。

关键入口：

- 生产入口是根目录 `main.go`。
- `config.InitConfig` 读取 `.env` 或 YAML。
- `share/bitcoin_rpc` 初始化 bitcoind RPC。
- `indexer.NewIndexerMgr` 创建全局索引管理器。
- `indexer/base` 负责连续扫块、分配 sat range、处理 reorg。
- `indexer/handle.go` 的 `processOrdProtocol` 串起 exotic、nft、ns、brc20、runes、ft 的协议处理。
- `rpcserver` 暴露 base、ordx、ord、bitcoind 相关 API。

重要注意：

- `cmd/main.go` 不是服务入口，更像临时工具/测试入口。
- `indexer/mpn` 和 `indexer/dkvs` 当前没有实际作为运行主流程使用。主流程使用 `MiniMemPool`，不要把这两个目录当成默认排查入口。
- `IndexerMgr` 是单例，并且通过 delayed DB buffer、Clone/Subtract 机制抗 reorg。改状态写入时要同时考虑实时内存、备份实例和落库窗口。
- `processOrdProtocol` 的模块顺序重要：exotic 先生成依赖信息，后续 nft/ns/brc20/runes/ft 依次处理转移。

## docs

路径：`~/github/docs`

定位：SAT20 中文协议和产品文档。它不是运行时代码依赖，但应作为理解业务语义、协议意图、术语和产品边界的上层依据。

主要结构：

- `readme.md`：总览 SAT20 是 BTC 原生资产发行和流通协议，核心是资产绑定聪、随聪流动。
- `issuance/`：资产发行协议 ORDX，包括协议、v2.0、ordinal、inscribe、资产发行模型、SAT Object、SNS、D-Indexer 和 FT/NFT/SFT/DID 场景。
- `circulation/`：资产流通协议 STP，包括协议、RSMC、动态通道、全资产支持。
- `satoshinet/`：SatoshiNet，包括原生性、安全性、POS、增强型 UTXO、智能合约、流动池、经济模型和场景。
- `SUMMARY.md`：文档目录，也是理解概念分层的好入口。

使用方式：

- 当代码里的命名、协议动作或资产模型不清楚时，先对照这里的中文描述。
- 当 `indexer`、`transcend`、`satoshinet` 的实现和预期语义有偏差时，这里可以提供产品层判断，但最终仍需以当前代码行为为准。

## docs-en

路径：`~/github/docs-en`

定位：`docs` 的英文版本，结构和主题基本一致。它适合作为对外英文术语、接口文案和英文说明的参考。

主要结构：

- `readme.md`：SAT20 Protocol 总览。
- `issuance/`：ORDX / Asset Issuance Protocol。
- `circulation/`：STP / Asset Circulation Protocol。
- `satoshinet/`：SatoshiNet。
- `SUMMARY.md`：英文目录。

使用方式：

- 中文语义优先看 `docs`，英文命名、外部文档、README 或 API 描述可参考 `docs-en`。
- 如果两个文档内容不一致，不能自动假定英文更新；需要回到代码和用户确认。

## sat20wallet

路径：`~/github/sat20wallet`

核心模块路径：`~/github/sat20wallet/sdk`

模块：`github.com/sat20-labs/sat20wallet/sdk`

定位：SAT20 钱包 SDK 和客户端抽象层。它把 L1 indexer、L2 SatoshiNet/indexer、钱包管理、交易构造、签名、合约调用和 WASM 暴露组织在一起，是 `transcend` 等上层服务访问资产和链状态的重要依赖。

关键结构：

- `sdk/common`：配置、链、钱包、资产等共享类型。
- `sdk/wallet/manager.go`：钱包管理器，维护钱包状态、DB、L1/L2 indexer client、bootstrap/server node、UTXO locker、fee rate、reservation 等。
- `sdk/wallet/restclient.go`：定义和实现访问 indexer 的 REST client，包含 UTXO、资产摘要、ticker、broadcast、ascend、core node、DKVS 形状接口等。
- `sdk/wallet/types.go`：L1/L2 交易输出别名、节点类型、网络类型等。
- `sdk/contract`：amm、dao、launchpool、recycle、swap、transcend、vault 等合约相关运行时。
- `sdk/wasm`：面向 WASM 的包装入口。

关键关系：

- SDK 通过 replace 依赖本地 `indexer` 和 `satoshinet`。
- `transcend` 通过 SDK 管理钱包、连接 L1/L2 indexer、构造和签名协议交易。
- SDK 的 REST client 接口是理解上层服务如何消费 indexer API 的重要入口。

## transcend

路径：`~/github/transcend`

模块：`github.com/sat20-labs/transcend`

定位：SAT20 的 STP（Satoshi Transcending Protocol）服务。它负责跨 BTC 主网和 SatoshiNet 的通道协议、节点通信、资产锁定/解锁、open/close、splicing、stake、deposit/withdraw、contract 等流程。

关键入口：

- 根目录 `main.go` 是当前服务启动入口。
- `cfg` 从可执行文件目录读取 `conf.yaml`，默认 DB 为 `./data`。
- `stp.NewLightningCore` 和 `stp.NewSTPManager` 初始化核心服务。
- `rpc.InitRpcService` 启动 RPC：server 模式下开放协议 API，同时总是启动本地管理 API。
- 启动流程会解锁/导入钱包，初始化 STP manager，然后启动主网和 SatoshiNet 监控线程、心跳、regulator 等。

关键结构：

- `stp/define.go`：版本、DB 版本、协议消息常量、通道动作常量，且复用 `indexer/common.Decimal` 和 `sat20wallet/sdk` 类型。
- `stp/manager.go`：`STPManager`，维护 channel、reservation、wallet manager、L1/L2 indexer client、bootstrap/server node、fee rate、regulator、watchtower、UTXO locker 等核心状态。
- `stp/interface.go`：创建和初始化 STP manager，校验 chain/env，打开 DB，创建 wallet manager，连接 L1/L2 indexer。
- `rpc/router.go`：Gin RPC 路由，区分公开协议接口和本地管理接口。

关键关系：

- 依赖 `sat20wallet/sdk` 管理钱包和交易。
- 依赖 `indexer` 获取 L1 主网资产/UTXO/交易状态。
- 依赖 `satoshinet` 或 SatoshiNet 侧 indexer 获取 L2 状态。
- 它是把 `docs/circulation` 中 STP 概念落到服务流程里的主要代码。

## satoshinet

路径：`~/github/satoshinet`

模块：`github.com/sat20-labs/satoshinet`

定位：基于 btcd 改造的 SatoshiNet 节点，实现 BTC 原生资产的 L2/扩展网络环境。它和 `indexer` 的关系不是简单上下游：`indexer` 负责 BTC L1 资产索引，`satoshinet` 负责 SatoshiNet 网络本身及其 L2 侧状态。

关键结构：

- 整体目录保留大量 btcd 风格模块，如 blockchain、mempool、mining、peer、rpcserver、wire。
- `wire`、`txscript`、`btcutil` 等基础层扩展了资产相关能力。
- `mining/posminer` 体现 SatoshiNet 的 POS/出块逻辑。
- `anchortx`、`stp` 等目录与跨层、锚定或通道流程相关。
- `indexer/` 是 SatoshiNet 侧索引能力，包含 ascend、descend、core node 等 L2 特有查询。

SatoshiNet 内置 indexer 要点：

- `satoshinet/indexer/main.go` 提供 `NewIndexerMgr`，创建 indexer config、初始化共享 indexer、启动 RPC。
- `satoshinet/indexer/indexer/base/base_indexer.go` 维护 `AscendMap`、`DescendMap`、`coreNodeMap`。
- API 包含 ascend/descend/core node 查询，如 `/v3/ascend/:utxo`、`/v3/descend/:utxo`、`/v3/corenode/all`、`/v3/corenode/check/:pubkey`、`/v3/corenode/info/:pubkey`。

关键关系：

- `sat20wallet/sdk` 和 `transcend` 会同时关心 L1 indexer 与 SatoshiNet/L2 状态。
- `docs/satoshinet` 是理解 SatoshiNet 设计目标的上层依据。
- `satoshinet_debug`、`satoshinet_solo` 虽然也存在于 workspace，但本轮不纳入基础范围；涉及变体时需要另行确认。

## 协作时的默认判断

- 当前任务若涉及 L1 资产索引、Ordinals/ORDX/BRC20/Runes、UTXO/sat range/API 查询，优先看 `indexer`。
- 当前任务若涉及协议概念、产品语义、术语说明，优先看 `docs` 和 `docs-en`。
- 当前任务若涉及钱包、交易构造、签名、L1/L2 indexer client、WASM SDK，优先看 `sat20wallet/sdk`。
- 当前任务若涉及 STP、通道、open/close、lock/unlock、splicing、节点协议消息、跨层流转，优先看 `transcend`。
- 当前任务若涉及 SatoshiNet 节点、L2 出块/共识、ascend/descend/core node、SatoshiNet 内置索引，优先看 `satoshinet`。
- 不要默认把其他仓库的实现当作当前六个项目的事实来源；需要时再按任务单独确认。
