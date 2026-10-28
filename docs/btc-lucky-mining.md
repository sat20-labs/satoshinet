# BTC Lucky Mining

BTC Lucky Mining is an optional Bitcoin-native CPU mining module. SatoshiNet
nodes and wallets both fetch compact BTC mining jobs from the L1 indexer, mine
locally, and submit solutions back to the L1 indexer.

## Scope

- BTC Lucky Mining is controlled by `btcluckymining`, not by SatoshiNet PoS
  mining or `generate`.
- An ordinary wallet uses the channel address with its L1 indexer provider.
  A core uses its own indexer for mining data and the channel address with the
  bootstrap for co-signing. These are separate roles.
- The Bitcoin coinbase input script only includes the `satoshinet` tag and the
  extra nonce. Winner identity is determined by the coinbase reward address.
- Bitcoin block, transaction, header, getblocktemplate, and submitblock handling
  use `github.com/btcsuite/btcd/...` types, not SatoshiNet's extended wire
  types.
- The L1 indexer rebuilds the block from the cached job and submitted solution,
  verifies the hash target and reward address, then calls Bitcoin Core
  `submitblock`.
- Found block metadata is recorded in the L1 indexer's local DB under the
  `btclucky:found:` prefix.

## Flow

```text
Bitcoin Core / bitcoind
        ^
        | getblocktemplate / submitblock
        |
L1 indexer BTC lucky service
        ^
        | HTTP compact job / solution
        |
SatoshiNet node or wallet local miner
```

## SatoshiNet Configuration

```ini
btcluckymining=0
btcluckyminingjobs=1
btcluckymininglowpriority=1
btcluckymininglowprioritysleep=1ms
```

SatoshiNet nodes use the configured L1 indexer endpoint:

```ini
indexerscheme=http
indexerhost=127.0.0.1:8009
indexerproxy=testnet
```

The template service runs in the L1 indexer. Mining start/stop on a node is
controlled by its configuration.

## 奖励提升与共签提取

L1 indexer 同步到实际 coinbase UTXO 后，钱包先展示等待成熟的奖励。只有当前
L1 主链上的 coinbase 达到成熟度，已有的 L1 监控流程才会将奖励全额 ascend
到原来的 2-of-2 地址。客户端签名前、服务端共签入口及客户端广播前均校验来源
和成熟度；ascend 不花费原始 Bitcoin 输出。未成熟 coinbase 不计入通道可用
抵押余额；已成熟但尚未 ascend 的 Lucky 奖励也不计入公共可用抵押额度，
不能间接支持其他映射或释放已有抵押。只有验证该奖励自身的专用 ascend 时，
才将本次来源计入额度；复用现有通道账本识别已映射来源，不增加持久状态。
奖励发现复用 L1 地址 UTXO
与已有通道账本，不依赖最近中奖记录窗口；
原始 coinbase 被 splicing 找零替换后，已映射奖励仍可发现。

用户提取时，客户端和服务端重新校验原始 coinbase、当前 L1 确认数、anchor
来源及其未花费输出。成熟度取自 L1 indexer 的 `share_rpc.bitcoin.coinbase_maturity`
配置，并由 `/btc/lucky/info` 返回；配置或成功响应未提供成熟度（或为 0）时，
使用 Bitcoin 默认值 100；请求、解析或服务错误仍返回错误。隔离私有链配置为
10。成熟度按原始 L1 奖励计算，不从 ascend 重新计时。claim 继续复核成熟度，
不增加第二轮等待。claim 收到远端签名后、广播前再次验证成熟度、来源、
未花费的 L2 输出及通道抵押状态，并使用重新获取的 prevouts 完成签名验证。
成熟不构成绝对最终性，极深 L1 重组仍可能影响已映射抵押。

等待成熟期间若奖励被 L1 重组移除，不生成其 L2 anchor。单条来源或状态查询
失败时，奖励列表保留该条 `status: "error"` 及 `error` 原因，其他奖励继续
发现和处理；列表、配置或通道账本整体读取失败仍返回错误。未 ascend 的 Lucky
coinbase 不能通过普通 L1 unstake、sweep 等共签请求提现或重新包装以规避分账。
已映射抵押的 L1 流转仍受原有通道额度和 deanchor 校验约束。

提取交易花费 anchor 的第 0 个输出，要求两方真实签名；扣除
`DEFAULT_FEE_SATSNET`（当前 10 sats）后，用户、服务节点、引导节点按
85% / 10% / 5% 分配。10% 属于实际提供挖矿数据的索引器节点；核心节点使用
自己的索引器时，85% 与 10% 均归自己，bootstrap 只收取 5% 网络基础服务费。
整数余数归用户，相同收款脚本合并。核心节点使用引导节点作为共签方；引导节点
不作为中奖者，不能通过调换 2-of-2 双方身份改变分账。
已由普通通道管理或预留的 UTXO 不允许走此路径。

PWA 的 Lucky Mining 页面提供成熟进度与提取按钮，WASM 接口为
`getBTCLuckyRewards`、`claimBTCLuckyReward(fundingUtxo)`。节点运营者可通过
既有本地管理面调用 `GET /btc/lucky/rewards` 和 `POST /btc/lucky/claim`
（JSON 字段 `fundingUtxo`）；这些路由不在公开协议服务上注册。

这条提取路径验证的是奖励映射后在聪网的共签花费。原始 L1 coinbase 仍作为
映射抵押，回到 Bitcoin L1 的流程需要相应的 deanchor。

## 聪网中奖索引与查询

Lucky 功能尚未上线，使用唯一的新格式，不提供历史 Lucky 格式兼容或回填。
奖励 anchor 除原有输出外，增加一个零金额、无资产的 STP OP_RETURN，类型为
`CONTENT_TYPE_BTC_LUCKY`（10）。载荷固定 37 字节：版本 `1`（1 字节）、
Bitcoin 高度（4 字节 little-endian）、Bitcoin 区块哈希（32 字节内部字节序）。
奖励 outpoint、金额和双方公钥从现有 anchor 取得，不重复写进载荷。

标识必须由启用输出绑定的 anchor invoice 签名认证。Lucky 专用签名与普通
ascend 签名入口校验真实 coinbase、当前 L1 来源区块、完整奖励金额、输出脚本及
双方角色；真实 Lucky coinbase 省略标识也会拒绝。旧通用 L2 共签入口按真实父
交易识别 Lucky 输入，包含混合输入时同样要求使用专用 claim，不能绕过成熟度。重复、畸形、未知版本或非零金额的标识被拒绝。
标识是服务节点核验 L1 事实后的签名声明，不是独立的 Bitcoin PoW 证明。
奖励提取同样要求此标识且重新核验当前 L1 来源；ascend 与提取均要求成熟。

聪网索引器将标识保存在原 `AscendData.BTCLucky` 中，复用现有落库、快照和
回滚路径。没有独立成功账本、累计计数、后台同步或回填任务。普通 ascend
没有此标识，不参与 Lucky 统计；奖励被提取后，成功记录仍保留。

```http
GET /{network}/v3/btc/lucky/blocks?pubkey={compressedMinerPubkey}&start=0&limit=20
```

`pubkey` 可省略以查询全部矿工；存在时要求有效的 33 字节压缩公钥十六进制。
`start` 从 0 开始，`limit` 为 1–1000，默认 20。记录按聪网高度倒序、同高度
按 funding outpoint 排序。空列表返回 `items: []`，无效参数返回 HTTP 400，
索引读取失败返回 HTTP 500，不用空列表掩盖错误。

响应的 `data` 包含 `indexedHeight`、`total`（符合筛选条件的奖励记录数）、
`minedBlockCount`（按 BTC 区块哈希去重的成功块数）、`totalRewardSats`（奖励
全额，包含所挖块的交易费，尚未扣提取费用）、`start`、`limit` 和 `items`。
`servicePubKey` 表示实际数据服务方；核心节点自挖时等于 `minerPubKey`，
不是 bootstrap 共签公钥。每条记录包含 `btcHeight`、`btcBlockHash`、`fundingUtxo`、`rewardSats`、
`minerPubKey`、`servicePubKey`、`rewardAddress`、`l2Height`、`anchorTxId`。
汇总基于整个筛选结果，不只针对当前分页。

统计口径是聪网上已确认的 Lucky 奖励 anchor；尚未 ascend 的 L1 中奖不在
此接口中。按上述签名策略，只有成熟后完成 ascend 的奖励才进入统计，不等待
提取。L2 重组随现有索引快照
回退，查询不会返回高于该快照的记录。后续 Bitcoin 重组不会自行删除一个
仍在 L2 链上的 anchor；消费方可用保存的 `btcHeight/btcBlockHash` 复核 L1
主链状态，提取端始终重新验证来源。

初版查询扫描现有 ascend 记录并合并当前未落库快照；没有新增数据库索引。
历史量增大后若有性能需要，再单独评估按高度或矿工建立查询索引。
