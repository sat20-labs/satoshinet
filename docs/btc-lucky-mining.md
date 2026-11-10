# BTC Lucky Mining

SatoshiNet 节点和 PWA 钱包使用同一个 Go miner。节点原生运行，PWA 通过钱包
SDK 的 WASM 入口运行。节点的 `btcluckymining` 独立控制 Bitcoin CPU 挖矿。
普通矿工的 coinbase 支付给自己与 L1 服务节点的 2-of-2 地址。核心节点使用
自己的 L1 indexer，以 bootstrap 为共签方。节点在聪网链和索引同步后校验身份。
对普通矿工提供模板的 L1 indexer 需将配置顶层的 `pubkey` 设为实际服务核心节点的公钥。
留空时 `/v3/indexer/pubkey` 返回 bootstrap 公钥，普通矿工会在启动前报告身份错误。

## 模板、挖矿与广播

请求时，在包含 proxy 前缀的 L1 indexer 地址后拼接以下路径。

1. 矿工从 L1 indexer 获取 `GET /btc/lucky/template`。同一模板的
   响应对所有矿工相同，包含父块、版本、时间、bits、target、coinbase 金额、
   witness commitment、按顺序排列的完整交易和 coinbase 的 Merkle 分支。
   矿工轮询时发送上次的 ETag。模板相同，indexer 返回 HTTP 304。
2. 矿工从随机的 64 位 extraNonce 起点给本地 worker 分配不同序列。
   每个 worker 用高度、`satoshinet` 标记、extraNonce 和奖励地址构造
   coinbase，沿 Merkle 分支更新根，再遍历区块头 nonce。获取下一份模板时，
   已下载的模板仍可计算。父块变化后，旧模板可能失效。
3. 命中后，矿工把包含 witness 的完整 Bitcoin 区块通过
   `POST /v3/bitcoin/block/submit` 提交。请求采用 BIP22 的 JSON-RPC 2.0
   `submitblock`，`params` 的第一个参数为完整区块的十六进制字符串。
   接受时 `result` 为 `null`，拒绝时为原因字符串，RPC 错误放在 `error`。
   indexer 无需查询 job 或模板，
   直接调用 Bitcoin Core 的 `submitblock`。广播暂时不可达时，运行中的矿工
   把同一完整块提交给备用 indexer。区块被明确拒绝时返回拒绝原因。
   网关 503、响应截断、回执无法匹配、节点暂缺父块时，均尝试备用端。所有广播端暂不可达时保留完整块并重试。
   收到接受或 duplicate 回执后，向同一个 indexer 核对 coinbase 高度的主链块哈希；侧链块不计入矿工的成功记录。
   模板下载和完整块上传的默认总时限都是两分钟。停矿会取消正在传输的请求并释放内存中的候选块。
4. 备用 indexer 可独立接收完整区块。原 indexer 恢复并同步后，
   钱包通过 coinbase UTXO 发现奖励。`GET /btc/lucky/info` 的
   近期中奖记录只包含经此 indexer 提交并接受的 Lucky 区块。

## 节点配置

```ini
btcluckymining=0
btcluckyminingjobs=1
btcluckymininglowpriority=1
btcluckymininglowprioritysleep=1ms

indexerscheme=http
indexerhost=127.0.0.1:8009
indexerproxy=testnet
```

节点和 PWA 均通过 indexer 的 HTTP 接口提交完整区块。节点沿用上面的
indexer 配置，`https://apiprd.ordx.market` 和 `https://apiprd.sat20.org`
互为备用，保持同一网络路径。PWA 有 `SlaveIndexerL1` 时使用其中配置的
备用 indexer。奖励公钥由提供模板的主 indexer 确定，启动前校验对应 STP 共签方已配置。PWA 固定以主钱包的第一个账户（账户索引 0）挖矿、接收和提取奖励。切换到其他钱包或子账户后，同一矿工继续运行。私网 PWA 在现有 `SlaveIndexerL1` 中指定内部备用服务。

```json
{"jsonrpc":"2.0","id":"block-hash","method":"submitblock","params":["完整区块hex"]}
```

## 奖励提升与共签提取

L1 indexer 同步到实际 coinbase UTXO 后，钱包先展示等待成熟的奖励。只有当前
L1 主链上的 coinbase 达到成熟度，已有的 L1 监控流程才会将奖励全额 ascend
到原来的 2-of-2 地址。客户端签名前、服务端共签入口及客户端广播前均校验来源
和成熟度；ascend 不花费原始 Bitcoin 输出。奖励由通道外管理，ascend 不会
自动将其纳入通道承诺。

原始 L1 Lucky coinbase 通过 coinbase 来源及 `satoshinet` 标记识别；原始
L2 anchor 使用 `CONTENT_TYPE_BTC_LUCKY` 标识并复核其 L1 来源。无论是否成熟、
是否已 ascend 或已 claim，原始 L1 输出始终排除在普通通道选币、扩容和通道
可用资产汇总之外，也不计入公共可用抵押余额。未 claim 的原始 L2 anchor
同样不能直接纳入通道或通过普通 L2 共签花费。相关汇总按请求过滤这些 UTXO，
不建立独立负债或公共额度模型，也不新增持久化余额库。

后台根据当前身份和 L1 已索引高度的区块哈希检查地址 UTXO，成功扫描后在下一次链尖或身份变化时再扫描。
查询或广播暂时失败时，下一轮可重试；钱包重启后从当前 UTXO 重新发现奖励。奖励列表另外读取已有通道账本以展示历史。
奖励校验要求交易报告的所属块哈希与同高度的 L1 主链哈希一致。
原始 coinbase 保留在其原始 outpoint，不通过 splicing 找零替换；已映射奖励
可通过既有通道账本继续发现。原始 L1 输出已花费或来源无法验证时，奖励条目
显示错误并拒绝提取。

用户提取时，客户端和服务端重新校验原始 coinbase、当前 L1 确认数、anchor
来源及其未花费输出。成熟度取自 L1 indexer 的 `share_rpc.bitcoin.coinbase_maturity`
配置，并由 `/btc/lucky/info` 返回；配置或成功响应未提供成熟度（或为 0）时，
使用 Bitcoin 默认值 100；请求、解析或服务错误仍返回错误。隔离私有链配置为
10。成熟度按原始 L1 奖励计算，不从 ascend 重新计时。claim 继续复核成熟度，
不增加第二轮等待。claim 收到远端签名后、广播前再次验证成熟度、来源、
当前 L1 区块身份、原始 L1 与 anchor L2 输出的未花费状态，以及两层输出均未
被普通通道管理或预留，并使用重新获取的 prevouts 完成签名验证。
成熟不构成绝对最终性，极深 L1 重组仍可能使已映射奖励失去有效的 L1 来源。

等待成熟期间若奖励被 L1 重组移除，不生成其 L2 anchor。单条来源或状态查询
失败时，奖励列表保留该条 `status: "error"` 及 `error` 原因，其他奖励继续
发现和处理；列表、配置或通道账本整体读取失败仍返回错误。普通 L1 unstake、
sweep 等共签请求始终排除原始 Lucky coinbase，包括已 ascend 或已 claim 后，
防止绕过分账或消耗映射的 L1 来源。

提取交易花费 anchor 的第 0 个输出，要求两方真实签名；扣除
`DEFAULT_FEE_SATSNET`（当前 10 sats）后，用户、服务节点、引导节点按
85% / 10% / 5% 分配。10% 属于实际提供挖矿数据的索引器节点；核心节点使用
自己的索引器时，85% 与 10% 均归自己，bootstrap 只收取 5% 网络基础服务费。
整数余数归用户，相同收款脚本合并。核心节点使用引导节点作为共签方；引导节点
不作为中奖者，不能通过调换 2-of-2 双方身份改变分账。
已由普通通道管理或预留的 UTXO 不允许走此路径。

claim 产生的输出是普通 L2 资产，仍由通道外管理；需要纳入通道时，再使用
既有的显式 lock 流程。原始 Lucky coinbase 与未 claim 的 anchor 均不作为
普通通道操作的候选输入。

`LockToChannelWithExpand` 优先 expand 私有通道地址中尚未纳入管理、未被预留
且可扩容的普通 L1 目标资产 UTXO，再 lock 现有远端余额，最后从公共通道增资
缺口部分。本地 expand 沿用整条 UTXO 的处理方式，可能超过本次请求金额。
首次开通道前已有的 L2 资产会保留；重开前的清理只处理已确认关闭交易及其
配对 deanchor 证明绑定的 L2 输入，依据现有关闭材料验证，不按地址总余额销毁，
也不新增迁移额度记录或关闭 payload 字段。

PWA 的 Lucky Mining 页面提供成熟进度与提取按钮，WASM 接口为
`getBTCLuckyRewards`、`claimBTCLuckyReward(fundingUtxo)`。节点运营者可通过
既有本地管理面调用 `GET /btc/lucky/rewards` 和 `POST /btc/lucky/claim`
（JSON 字段 `fundingUtxo`）；这些路由不在公开协议服务上注册。

这条提取路径只花费聪网 anchor，不花费或解除原始 L1 coinbase 的隔离。
当前 Lucky 流程没有原始 coinbase 的 L1 提现或重新包装操作。

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
