# 模板合约 E2E 测试与全仓门禁

## 执行入口

四个注册模板：`limitorder.tc`、`amm.tc`、`exchange.tc`、`autopay.tc`。
模板用例及共用夹具没有外层 `//go:build rpctest`，普通 SatoshiNet 全仓命令会发现并执行：

```bash
go test ./... -count=1 -timeout=45m
```

原来的 `make unit` / `make check` 使用 `rpctest`，仍包含这些测试。
现有 `dkvs-dev-bootstrap` 全仓回归步骤保持无标签执行，设置 45 分钟包级超时，与 Makefile 的根模块测试一致；CI 作业总超时为 60 分钟。
跨输出上限的场景需要 1001 笔真实委托交易，逐批确认后再验证关闭，因此全仓门禁需要覆盖该运行耗时。
EVM、Agent、POS 专项测试保留 `rpctest`；模板与 EVM 同块顺序测试也保留该标签。
夹具内部构建节点仍使用 `rpctest,wallet_plugin`，这不限制外层测试发现。

只执行模板网络用例：

```bash
go test ./integration/contract_e2e \
  -run '^TestNetwork(Template|LimitRefundBuy$|AMMSameBlockSequentialPricing$)' \
  -count=1 -timeout=45m -v
```

只执行本次新增和修正的用例：

```bash
go test ./integration/contract_e2e \
  -run '^TestNetworkTemplate(LifecycleMatrix|LimitOrderScenarioMatrix|AMMLiquidityScenarioMatrix|ExchangeScenarioMatrix|AutopayScenarioMatrix|AutopayCloseAcrossOutputLimit|AMMDefaultSwapAndCloseOwners|DeploymentRejectsInvalidContent|RefundRejectsNonNumericOpcode|AMMWaitsUntilAddLiquidityMeetsK)$' \
  -count=1 -failfast -timeout=45m -v
```

运行需要 Go、CGO 和可构建 Go plugin 的系统，及相邻的 `indexer`、`sat20wallet/sdk` 本地模块。
夹具自行启动隔离节点和模拟 L1 索引接口，不使用远程测试网的共享钱包、余额或链数据库。
不以环境变量、`testing.Short()` 或 `t.Skip()` 静默跳过模板门禁。
普通 `go test ./...` 不跨越相邻 SDK 的独立 Go module；SDK E2E 需在 SDK 目录执行。

## 实现位置与校验方式

| 文件 | 内容 |
| --- | --- |
| `integration/contract_e2e/template_network_e2e_test.go` | 现有订单撮合、部分成交、定向退款、AMM 同块顺序定价、初始池就绪、滑点、流动性及 Exchange 默认调用 |
| `integration/contract_e2e/template_scenarios_e2e_test.go` | 新增四模板生命周期、参数拒绝、部署拒绝、资金边界及业务场景 |
| `integration/contract_e2e/template_autopay_batch_e2e_test.go` | 超过单次 Result 可退款输出数量的真实委托人批量关闭 |
| `integration/contract_e2e/template_scenarios_helpers_test.go` | 复用双节点夹具；真实 BIP86/Schnorr 签名、找零、脚本校验及公开查询 |
| `integration/contract_e2e/network_helpers_test.go` | 从原 anchor 测试抽出的共用节点、L1 夹具和资产工具 |
| `integration/contract_e2e/wallet_runtime_test.go` | 钱包插件及隔离节点程序构建 |
| `contract/template_codec_test.go` | 非整数退款操作码拒绝、合法 ID 编码及长度/负数/截断边界回归 |
| `contract/template/amm_proportion_regression_test.go` | AMM 移除/关闭与成本分摊的精度缺陷回归，生产修复待审核 |

新增场景必须经过交易构造、签名、广播、真实出块、模板 Result 和公开状态/余额查询。
不直接写入 RuntimeStore 或数据库，不在测试进程内执行 backend 代替节点结算。
部分旧夹具仍使用简化的可花费脚本；新增场景验证每个输入的真实 Schnorr 签名。

模板 Result 的 `Status=Success` 表示整块结算交易有效。业务调用被拒绝时也可能包含在成功结算的 Result 中。
拒绝用例还断言本金精确退回、订单/池/委托人状态不变或调用计数不增长；不能仅检查广播成功或 Result 状态。
因此 SDK 现有“非法资金订单”“非部署人 close”的 Result Success 预期无需改成 Invalid。

## 覆盖矩阵

| 模板/维度 | E2E 场景 |
| --- | --- |
| 四模板公共生命周期 | 部署；未知 action；每个公开 action 的畸形参数；非部署人 close；部署人 close；重复 close；关闭后默认注资及所有公开 action 退回资金；永久不可关闭标志；同数据目录重启；列表、历史和 RPC 状态查询 |
| 部署边界 | 非法资产名；AMM K 不一致、零储备；Exchange 同资产、阈值未排序；Autopay 零最低费率；本金退回且不存在可调用 runtime |
| LimitOrder `swap` | 买卖撮合；大单由小单累计填充；价格优先；默认买入；零/负数量、零价格、资产不匹配、卖方实际注资不足；部分成交；买方价格改善及超额支付退回；不交叉订单留在订单簿 |
| LimitOrder `refund` | 全部自有订单退款；指定 ID 退款；部分成交后的剩余资产退款；其他调用人不能退走订单；重复退款不得再退本金 |
| LimitOrder `close` | 买卖两侧未成交本金分别退回其所有者 |
| AMM `swap` | 买入、卖出；默认买卖；同块按更新后的储备顺序定价；初始池未就绪时等待，补足后下一块成交；最小输出及最小卖价不满足时全额退回；精确储备和 K |
| AMM `addliq` | 补足初始池；多 LP；不平衡注资退回多余资产；实际资产或聪不足不改变池和 LP |
| AMM `removeliq` | 无 LP；部分移除；超过持有 LP 时按实际余额移除；重复移除不重复付款；移除全部自有 LP；返回精确本金且不改变其他 LP 权益 |
| AMM `close` | 退回初始 LP；两个 LP 分别收到各自本金；清空池和 LP 状态；不支持旧 `refund` action |
| Exchange 默认调用/`exchange` | 注入库存；默认买入；显式买入；最低输出恰好满足或不满足；库存耗尽退回剩余付款；空库存全退；整数资产舍入退余款；`sold_a` 一次跨多个价格段、恰好落在阈值、后续调用按新价格；高度前后价格切换；聪付款与找零 |
| Exchange `close` | 退回剩余库存；权限、不可关闭及关闭后调用拒绝 |
| Autopay 默认调用/`config` | 注册委托人；最低费率、默认及最大 blob 限额；零/低于最低费率、超限、资产精度不匹配；gas-only 不得更改 blob 限额；只有部署人能配置运行 gas |
| Autopay 自动触发 | 同资产本金与运行 gas 隔离；一个触发预算只支付一次；gas 耗尽暂停；重启保持暂停及余额；补充 gas 恢复，不补扣暂停期间的块 |
| Autopay `cancel`/`close` | 退回剩余委托本金；重复 cancel 不重复退款；取消后重新配置和注资；部署人关闭；超过 998 个委托人分块退款，每个 Result 不超过共识输出上限，每人只退一次 |

AMM 原“等待补池”用例去掉了不支持的 `refund` 触发和“余额大于零”断言。
补充流动性从 `10 资产 + 1 聪` 改为 `10 资产 + 3 聪`，与初始池阈值场景一致；随后要求买方余额从 900 变成 930，储备变成 70/33，真实成交数为 1。
当前 POS 在 mempool 为空时不出块。需要后续区块的场景通过普通签名转账推进高度，仍由节点执行模板结算。
普通转账同时支付聪手续费满足 mempool relay 策略；保留完整找零时仅扣 SGAS 不足以通过该策略。

## 覆盖边界与维护建议

- 这些用例覆盖当前四模板全部公开 action、默认调用、主要资金流和失败边界。有限用例不能证明“所有可能的应用场景”都正确。
- SDK 已有 ORDX 绑定资产、Runes/BRC20 多精度矩阵；它们在 SDK 独立 module 中。该矩阵不应被描述成 SatoshiNet 根目录全仓命令已执行的内容。
- 10000 委托人总容量上限、128 价格段上限及重复高度触发的直接状态边界，目前仍由模板包单元测试承担。新增跨输出上限用例使用真实委托交易，未用私有状态注入来伪装该上限的 E2E。
- 后续优先补资产精度/绑定聪在 SatoshiNet 默认门禁的矩阵，以及 AMM 成交盈利后移除/关闭时基金会与 LP 的精确分账。新增用例保持原夹具，不另建执行框架。
- 可以合并共享同一前置状态的用例以减少节点启动，但不得删掉失败断言或隐藏尚未通过的场景。

## 本次验证

只执行上面的新增/修正名单，不执行全仓测试。
默认门禁共 24 个模板网络顶层用例：22 个 `TestNetworkTemplate` 前缀用例，以及既有的 `TestNetworkLimitRefundBuy`、`TestNetworkAMMSameBlockSequentialPricing`。
默认及 `rpctest` 入口的编译检查、无标签发现列表和运行日志保存在：
`/private/tmp/satoshinet-template-e2e-20261009/`。
最终源码的默认入口和带 `rpctest` 入口编译通过；默认发现列表确认 24 项。
后续定向执行由 GPT 6 luna（max）负责，失败由主代理定位。

| 用例组 | 最新结果 | 证据 |
| --- | --- | --- |
| 四模板公共生命周期 | 48/48 叶子通过 | `run-standard-3.jsonl` |
| LimitOrder 场景 | 12/12 叶子通过 | `run-standard-3.jsonl` |
| Exchange 场景 | 9/9 叶子通过 | `run-remaining.jsonl` |
| Autopay 常规场景 | 16/16 叶子通过 | `run-final-targeted.jsonl` |
| 非法部署 | 6/6 叶子通过，分两轮验证 | 前 5 项见 `run-remaining.jsonl`，零最低费率见 `run-final-targeted.jsonl` |
| 退款非整数操作码 | 独立真实节点 E2E 通过；19 个解码单测叶子通过 | `run-remaining.jsonl`、`refund-codec-after.jsonl` |
| 修正的 AMM 等待补池 | 通过 | `run-standard-2.jsonl` |
| AMM 流动性场景 | 修复后 13/13 叶子通过 | `run-amm-fixed.jsonl` |
| AMM 默认买卖与双 LP 关闭 | 3/3 叶子通过 | `run-amm-fixed.jsonl` |
| Autopay 跨输出上限关闭 | 性能诊断轮通过：1001 人真实注资及跨块精确退款；仍有 ACK 超时 | `run-pos-profile.jsonl` |
| 最终源码编译 | 默认入口与 `rpctest` 入口均通过 | `compile-source-final-default.jsonl`、`compile-source-final-rpctest.jsonl` |

最后一次定向复验只运行 Autopay 的 16 项及零最低费率部署 1 项，17/17 通过，耗时 374.331 秒。
AMM 修复后的两组定向 E2E 为 16/16 通过，耗时 203.501 秒。
本次验证分轮累计 110 个新增/修正 E2E 叶子通过，未执行全仓测试。
功能断言通过不表示性能达标：批量用例仍有超过 1 秒的区块处理和 ACK 超时。

`run-standard-3.jsonl` 中四模板生命周期 48 个叶子、LimitOrder 12 个叶子通过。
AMM 在超额移除场景停止：新增测试原先错误预期“余额不变”，而协议明确规定按调用者实际 LPT 余额移除。
按协议修正为精确退款及其他 LP 权益校验，并补部分移除、重复移除；后续定向执行发现下面记录的份额精度问题。

首轮多委托人一次广播 1001 笔注资交易，出现 POS 确认超时，未进入关闭断言。保留日志 `run-new.jsonl`；该用例改为每 20 笔逐批确认，仍创建 1001 个真实委托人并保持完整退款断言。

### 已批准的最小修复：退款 ID 解码接受非整数操作码

`run-standard-2.jsonl` 中 `limitorder.tc/malformed_refund_refunds_without_mutation` 失败：参数 `0xFF` 被接受，调用计数由 0 增为 1。
根因：`TemplateRefundInvokeParam.Decode` 只检查数据长度和数值正负；`ScriptTokenizer.ExtractInt64` 对没有数据的非整数操作码返回 0。
因此非法操作码可以被解释为订单 ID 0。独立失败回归 `TestNetworkTemplateRefundRejectsNonNumericOpcode` 保留在默认门禁中，要求不取消原订单、退回新调用本金。

用户已批准并实施最小方案：仅在退款 ID 解码入口检查操作码是否为整数推送，保留合法编码、现有长度/非负校验和调用人权限。
`refund-codec-before.jsonl` 证明 7 种非整数操作码均错误接受；合法整数编码及负数/长度/截断边界回归通过。
`refund-codec-after.jsonl` 中三个新增单测、19 个叶子全部通过；Luna 已在 `run-remaining.jsonl` 中确认独立真实节点 E2E 通过。
常规模板用例使用明确截断的 `OP_PUSHDATA1` 校验畸形参数拒绝，与非法操作码缺陷分别记录，不删除缺陷复现用例。

尚未通过的门禁不能作为发布准出依据。

### 已批准修复：AMM 份额中间截断

`run-standard-4.jsonl` 的 `partial_remove_preserves_remaining_LP` 失败：池子为 106/106、总 LP 为 106，移除 2 LP，实际只收到 1 个资产；正确份额为 2。
`applyAMMLiquidity` 先计算并截断 `2/106`，再乘池余额，导致应为整数的结果少一个最小单位；`proportionalInt64` 的聪和成本分摊、关闭时的资产分摊有同类计算。
保留精确金额断言，新增 0/6/10 位资产精度、成本、关闭与比例边界回归。
用户于 2026-10-10 批准最小修复。仅在 AMM 的份额计算中采用整数单位先乘后除、最后取整，保持资产精度及现有利润分成、权限、持久化结构。
局部 `proportionalDecimal` 先将 LP 分子/分母对齐精度，再乘资产整数单位并除以 LP 总量；用于移除及关闭的资产、聪和本金成本分摊。不更改全局 Decimal 运算或利润分成计算。
`amm-proportion-before.jsonl` 中上述 9 个回归失败，另 6 个比例边界场景通过。
Luna 修复后定向单测 `amm-proportion-after.jsonl` 中 15/15 叶子通过；两组 AMM E2E 在 `run-amm-fixed.jsonl` 中 16/16 叶子通过。

`run-standard-5.jsonl` 的 Exchange 前 7 个场景通过，高度切换因推进区块的普通转账未支付聪手续费而被 mempool 拒绝。
测试夹具已补显式聪手续费，保留业务断言。其余 5 个独立顶层用例在 `run-remaining.jsonl` 中使用一次运行时构建验证，不包含待审核的 AMM。

`run-remaining.jsonl` 中 Exchange 的全部 9 个场景和独立退款操作码 E2E 通过。
Autopay 重复取消因选币未包含网络 gas 而在签名前失败；已将实际 gas 加入选币需求，保持交易本金和断言不变。
Autopay 零最低费率部署被高层构造器提前拒绝；已改用公开低层部署构造器和显式非法内容，让该用例验证节点端拒绝。
这两项测试构造修正由 Luna 在 `run-final-targeted.jsonl` 中定向复验通过，未放宽资金或状态断言。

### 待定位性能问题：POS 区块验证与入链耗时

每 20 笔确认的批量关闭用例在确认 700/1001 个委托人后停止，尚未进入关闭断言；保留 `run-remaining.jsonl`。
等待超时的真实状态是 bootstrap 高度 39、mempool 20 笔、目标交易未确认，core 高度 40、目标交易已确认。
`ValidatorManager` 固定等待 ACK 2/4 秒；接收端在完整区块检查和 `ProcessBlock` 完成后才回复 ACK，本轮日志显示约 9 秒处理时间。
发送端等待超时返回，未执行本地 `SubmitNewBlock`；接收端已入链，之后出现从旧 tip 提案被拒。
原建议调整两级 ACK 等待上限为 30/60 秒；用户要求先检查处理为何耗时约 9 秒，正常应控制在 1 秒以内。该超时方案未批准，现有 2/4 秒保持不变。
复现诊断复用节点现有 CPU profiler，并临时记录模板验证和 `ProcessBlock` 分段耗时；采样结束后恢复临时计时代码和 profiler 启动参数。性能生产修复仍需先给出根因和最小方案，由用户审核。
上述早期失败轮不能标记为“批量关闭退款通过”。后续诊断轮 `run-pos-profile.jsonl` 实际通过全部 1001 人注资、至少两个区块退款、每人仅退一次及关闭后状态断言，用时 741.70 秒；未改变 ACK 时限或业务断言。

### POS 性能采样结论与待审核方案

采样复现了接近 9 秒的处理：core 高度 52 的模板验证为 2.924 秒、`ProcessBlock` 为 5.758 秒，合计 8.683 秒。
63 个完成两阶段计时的区块中，48 个合计超过 1 秒；bootstrap 记录 40 次实际 ACK 超时事件。
部分超时后两端通过普通区块同步追上，本轮功能 E2E 最终通过；不能据此认为提案确认路径正常。

CPU 样本以整个采样期间为分母，累计调用栈时间相互包含，不能相加：

| 热点 | core | bootstrap |
| --- | --- | --- |
| `ContractRuntime.loadRuntimeState` | 62.09 / 114.69 CPU 秒，54.14% | 168.15 / 307.47 CPU 秒，54.69% |
| core 完整合约验证 | 74.81 CPU 秒 | — |
| core 状态解码占完整合约验证 CPU 的比例 | 约 83% | — |

主 CPU 根因是每笔默认注资重复解码整份模板状态，包括已累计的所有委托人：框架生命周期查询、backend 生命周期检查、容量检查和 `ApplyDefaultInvoke` 分别加载状态；结算及余额处理还会继续查询。每次加载又经过自定义 JSON 解码，产生大量分配和 GC。小规模 AMM 对照区块验证约 2.6 毫秒、入链约 77 毫秒。
正式入链重新验证合约是现有安全边界：区块 hash 不绑定 witness，且提案后保留的执行状态可能已释放；不能通过删掉正式验证来达到耗时目标。
本次采样定位了主要 CPU 热点；没有把所有墙钟耗时都归为 JSON，锁等待、调度和落库的余量应在局部优化后继续测量。

建议先实施不增加缓存机制的局部收敛：单次调用内将生命周期、容量及注资处理共用一次解码的状态，保留原校验、正式入链验证、JSON 持久格式及失败时不提交状态的语义。
由 Luna 复验原资金/权限场景和同一批量用例，并再次测量验证与入链时间。该第一步不能提前保证最坏耗时低于 1 秒；若仍未达标，再提交块内工作状态复用方案，明确快照隔离、失败回滚及维护成本，由用户决定。
性能生产修改待用户审核，未实施。ACK 时限仍为 2/4 秒。

原始证据在 `/private/tmp/satoshinet-template-e2e-20261009/`：`pos-core.pprof`、`pos-bootstrap.pprof`、完整节点日志、`pos-cpu-core-cumulative.txt`、`pos-cpu-bootstrap-cumulative.txt`、`pos-stage-summary.json`。
采样后临时计时代码和 profiler 启动参数已恢复，两个文件与采样前逐字节一致。

### 入链分段调查（2026-10-10）

按用户要求，单独调查 `ProcessBlock` 的墙钟耗时。由 GPT 6 Luna Max 仅执行原有 `TestNetworkTemplateAutopayCloseAcrossOutputLimit` 一次；保留 1001 人真实注资、跨块退款及所有断言，不改 ACK 时限。本轮仅增加临时分段日志，没有启动 CPU profiler。

结果：用例通过，耗时 412.88 秒（包 414.57 秒），stderr 为空。bootstrap 仍记录 16 次实际 ACK 超时；功能通过依然不表示确认路径性能达标。
core 的 57 个完整入链样本中，15 个超过 1 秒；中位数 0.723 秒，P95 为 1.490 秒，最大 1.615 秒。提案验证与入链合计最大 2.875 秒。

`ValidatorManager` 调用的 `ProcessBlock` 实际经过 SyncManager 队列、链状态检查、正式合约重验、落库、资产索引和通知；它并非仅计算写数据库时间。

core 最慢入链区块为高度 44、hash `87f9fc87a44647139405de741ba7f0388c1228dfa921de47fcff50bec100e1d5`。同一区块的墙钟分段如下：

| 阶段 | 耗时 |
| --- | --- |
| `ProcessBlock` 全程 | 1615.271 毫秒 |
| 正式连接校验 `checkConnectBlock` | 1499.576 毫秒，约占全程 92.8% |
| 原始区块落库 | 0.275 毫秒 |
| 主链原子 DB 更新 | 37.914 毫秒 |
| 其中合约状态复制、根计算及编码存储 | 29.927 毫秒，包含在上一行中 |
| 资产索引 | 38.792 毫秒 |
| 连接通知（含重新获取链锁） | 0.214 毫秒 |
| SyncManager 排队 / readiness / 链锁等待 | 6.954 / 4.871 / 0.283 微秒 |

上表父子阶段不能相加。该区块其余检查、调用及调度开销约 37 毫秒；全轮最大未单独覆盖的余量约 133 毫秒，没有发现秒级余量。
core 全轮正式连接校验累计 32.477 / 40.866 入链墙钟秒，约占 79.5%。前轮 CPU 样本另外显示，netsync 入链调用栈内合约验证占 37.44 / 38.87 CPU 秒，约 96.3%；两种统计口径不能混用。

后续提交也有独立成本，但本轮没有观察到数秒级提交停顿：

| 阶段 | core P95 / 最大 | bootstrap P95 / 最大 |
| --- | --- | --- |
| 原始区块 DB 更新 | 19.5 / 234.0 毫秒 | 19.7 / 405.2 毫秒 |
| 主链原子 DB 更新（含合约状态处理） | 43.4 / 112.6 毫秒 | 38.8 / 56.9 毫秒 |
| 资产索引 | 94.5 / 499.1 毫秒 | 148.1 / 631.5 毫秒 |
| 连接通知 | 0.305 / 0.425 毫秒 | 0.403 / 0.541 毫秒 |

core 的 499.1 毫秒索引峰值发生在高度 39 的 `performUpdateDBInBuffer` 批量提交时点；代码路径为 `ConnectBlock → updateDB → performUpdateDBInBuffer → CommitBackup/UpdateDB`，同步提交后还生成下一份备份。当前索引使用 Pebble，WriteBatch.Flush 调用 `Commit(pebble.Sync)`。该阶段含计算、锁和同步写盘，本轮未进一步分解其内部耗时，因此不能把 499.1 毫秒全归因于磁盘。
本用例未激活 POSV2，也未覆盖 POSV2 的额外 UTXO 强制刷新和 `database.Sync`，上述数字不能用于证明该路径的性能。

结论：已测入链的主要慢点仍是正式合约重验，与提案验证重复走到的模板整份状态解码热点一致；建议先实施前述待审核的局部状态复用，保留正式验证安全边界。索引的周期提交峰值应保留为后续观察项，若模板优化后仍妨碍 1 秒目标，再单独拆解并提交最小方案，不预先增加异步提交机制或降低持久性保证。
本轮没有复现前轮 5.758 秒入链峰值；不能以本轮较低峰值声称此前数秒差异已逐项解释或性能已达标。前轮缺少细分墙钟记录，仍保留其原始证据。

证据：`run-insertion-stages.jsonl`、`run-insertion-stages.stderr`、`insertion-core.log`、`insertion-bootstrap.log`、`insertion-stage-summary.json`，均保存在 `/private/tmp/satoshinet-template-e2e-20261009/`。
结束后六个文件的临时计时和日志保存钩子均与本轮诊断前备份逐字节一致；未实施性能生产修复，ACK 仍为 2/4 秒。

### 已批准的默认注资局部状态复用（2026-10-10）

用户批准先实施最小局部优化。本轮只修改默认注资的 backend 路径：`executeDefaultInvokeOutputTx` 读取一次运行状态，生命周期检查、Autopay 容量检查和实际注资共用该局部值；`CheckInvocationLifecycle` 和 `ApplyDefaultInvoke` 的公开入口仍自行读取状态，原调用方行为保持一致。

框架 `Backend.Lifecycle` 接口仅返回生命周期信息，不传递运行状态。因此本轮没有为跨框架复用增加接口、适配层或缓存：原成功默认注资中这四处整份解码由四次减为两次（框架一次、backend 一次），并未将整笔执行的所有解码压缩到一次。显式 Invoke、结算、状态根和正式入链重验没有扩大修改。
原容量检查的位置与校验次数保持不变，只改变状态来源；局部状态仍在所有检查和 item 构造成功后按原 JSON 格式保存，没有在 runtime/backend 上保存可复用的解码状态。

实现：`contract/template/backend.go`、`contract/template/lifecycle.go`、`contract/template/state.go`。
新增回归：`contract/template/default_invoke_state_regression_test.go`，覆盖复用给定状态而不重新读取、JSON/记账与公开入口一致、同笔多输出顺序更新、容量上限处既有委托人继续注资、超限退款、关闭/正在关闭/错误资产拒绝、损坏状态错误及整块失败不发布候选状态。

GPT 6 Luna Max 定向单测已通过 15 个叶子，包耗时 2.193 秒，stderr 为空，日志为 `run-state-reuse-unit-final.jsonl/.stderr`。
前两轮暴露并修正了新增测试自身的问题：临时 Address 值调用指针方法导致编译失败；夹具误用 InvokeBaseGas 而非实际扣费的 ResultBaseGas，导致本金多带 100。保留原本金 2000 断言，修正夹具后通过，未因此改变生产费用逻辑或放宽断言。

对应四种模板默认注资 E2E 已由 GPT 6 Luna Max 顺序执行通过：4 个顶层、21 个叶子，包耗时 261.834 秒，stderr 为空；日志为 `run-state-reuse-network.jsonl/.stderr`。随后单独执行 1001 人批量 E2E，通过 1 个叶子，1001/1001 注资完成，用例 382.34 秒、包 384.265 秒；日志为 `run-state-reuse-bulk.jsonl/.stderr`。

本轮功能验证合计 15 个单测叶子与 22 个 E2E 叶子通过，只运行上述受影响用例。批量用例的第一次计时如下：

| core 阶段 | 中位数 / P95 / 最大 |
| --- | --- |
| 正式连接校验 | 0.502 / 1.072 / 1.529 秒 |
| 主链连接（含原子 DB、索引与通知） | 0.078 / 0.377 / 0.992 秒 |
| 全部入链 | 0.681 / 1.747 / 2.409 秒 |

58 个完整入链样本中，13 个超过 1 秒；提案验证加全部入链最大 3.460 秒，bootstrap 仍有 10 次实际 ACK 超时。正式连接校验累计 29.008 / 40.136 入链墙钟秒，占约 72.3%。该局部优化已经保持功能正确，但尚未达到 1 秒目标。
前后测试耗时和尾部波动较大，且本轮工作区有既有的 peer inventory 修改，不能把两轮耗时差直接当作严格 A/B 的优化收益。

最高入链时点为高度 39：正式校验 1.301 秒、主链连接 0.992 秒，资产索引日志对应周期备份提交。随后由 Luna 仅重跑同一批量用例，临时拆分索引锁等待、裁剪、批次构建、同步 Flush，结果见下节。不启用 CPU profiler，不改变 ACK 时限或业务断言。


### 局部复用后的索引提交调查（2026-10-10）

GPT 6 Luna Max 仅再次执行 `TestNetworkTemplateAutopayCloseAcrossOutputLimit`：PASS，1001/1001 注资，用例 430.73 秒、包 432.825 秒，stderr 为空。没有执行其他测试或修改业务断言。本轮为同一叶子的重复诊断，不能重复计入新增用例数。

core 共 60 个完整入链样本，20 个超过 1 秒；中位数 0.744 秒、P95 2.345 秒、最大 3.250 秒。提案验证加入链最大 4.068 秒；bootstrap 记录 19 次实际 ACK 超时。正式校验最大 1.183 秒，累计 24.931 / 50.782 入链墙钟秒（约 49.1%）。后两轮没有 CPU profiler，也没有稳定 A/B 条件，不能把耗时变化归为局部优化效果。

高度 39 提交高度 19 的周期索引备份，内部计时如下（父子阶段不能相加）：

| 阶段 | core | bootstrap |
| --- | --- | --- |
| base 锁等待 | 0.935 微秒 | 0.444 微秒 |
| base 缓冲裁剪 | 0.644 毫秒 | 0.606 毫秒 |
| 地址预取 | 0.395 毫秒 | 0.686 毫秒 |
| 推荐人读取 | 0.038 毫秒 | 0.037 毫秒 |
| base 批次构建 | 29.057 毫秒 | 40.960 毫秒 |
| base `WriteBatch.Flush` | 947.166 毫秒 | 1003.812 毫秒 |
| base 提交全程 | 1160.992 毫秒 | 1363.462 毫秒 |
| contract `WriteBatch.Flush` | 38.375 毫秒 | 110.037 毫秒 |
| contract 提交全程 | 39.219 毫秒 | 110.671 毫秒 |

core 同一区块正式校验 635.242 毫秒、主链连接 1539.397 毫秒、入链 2345.267 毫秒。第二次周期提交（入链高度 59、备份高度 39）的 base Flush 为 core 279.648 毫秒 / bootstrap 22.337 毫秒，显示尾部波动明显。
代码已确认 Flush 对应 `../indexer/indexer/db/pebble.go` 的 `batch.Commit(pebble.Sync)`；两端均没有触发批次容量自动提交。本轮定位到同步提交调用的耗时，但其内部 WAL 队列、物理同步及线程调度尚未分别测量，不能将整个调用墙钟直接命名为物理磁盘耗时。base 提交全程还包含未单独测量的日志、清理和调度余量。

另外两处尚须保留：

- 初始化 1001 个地址的高度 2，正式校验仅 11.020 毫秒、主链连接 2515.795 毫秒；日志将大部分时间夹在 `prefetchIndexesFromDB` 的逐地址读取/生成日志区间。`generateAddressId` 本身仅增加内存计数，地址点查目前用 Pebble iterator `SeekGE`，并逐地址写 info 日志。该区间尚未拆分查询、对象构造、日志和调度，不能直接归为某一项。
- 最慢入链高度 55、hash `a4c35f089711d4cefde2b9a534b96bcf2fa1488c41b9b2f0d30ed9754a27d691`：全程 3250.031 毫秒，正式校验 888.566 毫秒、主链连接 254.512 毫秒，还有约 2106.953 毫秒未被这两段覆盖。本轮没有继续覆盖 SyncManager 排队、链锁、原始区块 DB、block-index flush 与完成通知，因此该余量仍未定位；不能用此前另一轮的微秒级队列样本排除这一次的等待。

结论与后续建议：已批准的默认注资局部状态复用及功能验证完成，1 秒性能目标仍未达到。继续优化前，应先在同一用例中把上述未覆盖余量和 Pebble 同步提交内部等待补齐，确认地址预取区间的主要成本；生产优化仍须提出具体最小方案由用户审核。不要通过增加 ACK 时限、删除正式校验、切换 NoSync 或异步提交隐藏问题。若剩余模板解码需要跨调用状态复用，必须单独说明新增作用域、快照/失败隔离及维护成本，由用户决定。

证据均在 `/private/tmp/satoshinet-template-e2e-20261009/`：`run-state-reuse-index.jsonl/.stderr`、`state-reuse-index-TestNetworkTemplateAutopayCloseAcrossOutputLimit-{core,bootstrap}.log`、`state-reuse-index-stage-summary.json`、`state-reuse-index-commit-summary.json`。
本轮结束后六个临时计时/日志保存文件已恢复至诊断前备份并逐字节核对一致；未留下 profiler 或诊断钩子。已批准的三个生产文件修改与新增回归保留在 changes；未执行 git add/commit/push，staged 为空，ACK 仍为 2/4 秒。

### 补齐入链阶段与 300 秒刷新验证（2026-10-10）

按用户要求继续定位原有约 2.11 秒余量。仍由 GPT 6 Luna Max 执行同一个 `TestNetworkTemplateAutopayCloseAcrossOutputLimit`，未运行其他 case，没有生产逻辑、ACK 或断言修改。

本轮补齐 SyncManager 排队、readiness、链锁、正式重验、原始区块 DB、接受/连接两处 block-index flush、主链原子 DB、资产索引、UTXO flush 与两类通知计时；正式重验保留。额外拆开 ffldb 的 DB.Begin/回调/Commit、区块文件写入/Sync、LevelDB OpenTransaction/填充/Commit，以及地址预取中的查询和逐地址日志。链内多个计时记录先收集再一次输出，以减少测量日志的干扰。

第一次正常执行通过：1001/1001 注资，用例 520.78 秒、包 523.495 秒（含本次节点/插件重新构建），stderr 为空。core 57 个完整入链样本，3 个超过 1 秒，最大 1272.927 毫秒；最慢区块中正式重验 637.868 毫秒、资产索引 581.298 毫秒，主要耗时已解释。全部样本的未覆盖余量最大 20.554 毫秒。bootstrap 仍有 2 次实际 ACK 超时。
此轮运行节点未到 300 秒就结束，没有触发 ffldb 周期刷新，因此不能确认上轮高度 55 的停顿来源。

第二次仅复验同一用例，临时复用刚编译的同一节点/插件运行文件，每轮仍创建全新 rpctest 节点与 DB。在最后注资和状态断言之后，仅增加 13.791 秒诊断等待，使节点生命周期超过 305 秒，再执行原 config/close。ffldb 的正常 300 秒阈值、1001 次真实签名注资、关闭/跨块退款及全部业务断言均保持。结果 PASS：用例 329.51 秒、包 333.39 秒，stderr 为空；重复诊断不增加新增 case 计数。

#### 已定位的独立慢点

1. **地址初始化的逐地址 info 日志**。第二轮 core 高度 2 预取耗时 1322.499 毫秒，1002 次 DB 查询累计 1.188 毫秒，1002 次 `generateAddressId` info 日志调用累计 1309.349 毫秒，约占预取 99.0%。同一区块资产索引 1361.604 毫秒、正式重验仅 7.116 毫秒、入链 1440.424 毫秒。这已复现一个与模板验证无关的秒级入链慢点。bootstrap 同一预取 528.961 毫秒，其中日志 511.144 毫秒。`log.go` 当前使用同步 `io.MultiWriter(os.Stdout, rotatelogs)`；日志调用计时还包括共享 logger 锁、格式化、两个输出与调度，不能把整个值仅算成磁盘写入。
2. **资产索引的周期 Pebble 提交**。正常第一轮 core 约 815 KB 批次 `Commit(pebble.Sync)` 335.472 毫秒、约 9 KB 合约索引批次 118.527 毫秒，同一区块资产索引全程 581.298 毫秒；第二轮两批次分别 285.720 / 19.123 毫秒。此前 947 / 1004 毫秒峰值仍保留，不能以较快的新样本认为同步提交尾部已消失。
3. **ffldb 的 300 秒周期刷新**。代码 `database/ffldb/dbcache.go` 的 `needsFlush` 使用 300 秒阈值；在触发它的当前写事务中，`commitTx` 先 `flush` 旧缓存，再单独提交当前事务。`flush` 同步区块文件后，经过 LevelDB `OpenTransaction → 填充 → Commit`，会阻塞当前入链。第二轮在高度 55 实际观察到该路径；详细数据见下表。LevelDB 的 OpenTransaction 可含写锁、memtable 旋转和 compaction 等待；Commit 可含 SST/manifest 操作和 compaction 等待，测得的是 API 调用墙钟，尚未拆成每个底层系统调用。
4. **正式模板重验仍有高成本**。第二轮 core 正式重验最大 1226.255 毫秒、bootstrap 最大 1762.241 毫秒，与此前模板整份状态解码热点一致。这与上述日志和周期提交是不同成本，局部状态复用没有消除全部解码。

#### 高度 55 的同一区块分段

core hash：`cf2367d3516079e50d36e7a4b04695214fd427012c936979e4c5260d98df8143`。

| 阶段 | 耗时 |
| --- | --- |
| 入链全程 | 762.851 毫秒 |
| 正式连接校验 | 255.110 毫秒 |
| 原始区块 DB Update | 447.232 毫秒 |
| 其中 ffldb Begin / 回调 / Commit | 0.006 / 0.034 / 447.132 毫秒 |
| 其中写入 855 字节原始区块 | 14.743 毫秒 |
| 其中区块文件锁等待 / File.Sync | 0.656 微秒 / 18.059 毫秒 |
| 其中旧缓存 LevelDB OpenTransaction / 填充 / Commit | 138.969 / 2.030 / 137.958 毫秒 |
| 其中当前事务 LevelDB OpenTransaction / 填充 / Commit | 0.007 / 0.007 / 134.259 毫秒 |
| 接受前 block-index flush | 0.314 毫秒 |
| 主链连接全程 | 59.573 毫秒 |
| 其中主链原子 DB / 资产索引 | 38.465 / 20.449 毫秒 |
| SyncManager 排队 / readiness / 链锁等待 | 16.751 / 3.447 / 0.340 微秒 |
| 完成接受与通知 | 0.013 毫秒 |
| 未覆盖余量 | 0.528 毫秒 |

父子行不能相加。该样本的原始区块落库成本主要位于元数据事务路径，File.Sync 只占约 18 毫秒；并非队列/链锁等待。bootstrap 的同期原始区块 DB 为 415.116 毫秒：File.Sync 19.603 毫秒，旧缓存 Open/填充/Commit 为 99.394 / 4.888 / 176.402 毫秒，当前事务 Commit 112.842 毫秒。

第二轮 core 的 57 个完整入链样本中，14 个超过 1 秒，最大 1440.424 毫秒；提案验证加入链最大 2653.277 毫秒，bootstrap 仍有 9 次实际 ACK 超时。所有样本未覆盖余量最大 105.884 毫秒。core 队列最大 0.265 毫秒、readiness 最大 0.024 毫秒、链锁等待最大 0.630 微秒；bootstrap 链锁最大 2.791 毫秒，均没有秒级等待。两轮测量有尾部波动，不能作为严格 A/B。

**证据边界**：本轮证明了 300 秒刷新在原始区块落库中引入独立停顿，并且上轮高度 55 的时点与该阈值吻合；但没有原样复现此前 2106.953 毫秒余量。上轮没有原始 DB 内部分段日志，不能把那个历史数值全部确认为 ffldb 刷新。新的完整分段中没有遗漏 2 秒级等待，本轮已定位可观测的慢阶段。

#### 后续建议与实施状态

- 将 `BaseIndexer.prefetchIndexesFromDB` 的逐地址 `Infof` 降到 `Debugf`，复用现有按区块日志；改动一行，不增加缓存/持久状态，可消除正常 info 级别下的 1002 次逐地址同步日志输出。逐地址明细仍可通过 debug 打开。用户已批准并实施，定向复验结果见下一节。
- ffldb 同次周期刷新目前对旧缓存和当前事务分别执行 LevelDB Commit，可审核合并为一次元数据事务的局部方案：维持旧数据到新数据的覆盖顺序、先同步原始区块文件、只在成功后清缓存，保留现有失败/恢复语义。可能减少一次约 134 毫秒的提交，但不能保证最坏耗时小于 1 秒；仍需先补失败边界回归并由用户确认，未修改实现。
- 模板剩余重复解码与 Pebble 尾部继续单独处理。上述建议不能代替模板验证优化，也不通过延长 ACK、取消校验、NoSync 或异步提交来降低指标。

证据目录仍为 `/private/tmp/satoshinet-template-e2e-20261009/`：`run-full-insertion.jsonl/.stderr`、`run-full-insertion-300.jsonl/.stderr`，两组 `full-insertion[-300]-TestNetworkTemplateAutopayCloseAcrossOutputLimit-{core,bootstrap}.log`，以及对应 `full-insertion[-300]-stage-summary.json`。
结束后 satoshinet 的 12 个临时修改文件和 L1 indexer 的 Pebble 文件共 13 个文件全部逐字节恢复，临时计时 helper 已删除；诊断等待和运行文件复用也已移除。已批准的局部状态复用及现有 case 保持。没有留下临时计时代码，没有 git add/commit/push，staged 为空。

### 地址、ffldb 与 Pebble 的数据边界（2026-10-10）

#### 地址初始化具体指什么

`BaseIndexer.prefetchIndexesFromDB` 遍历新块的交易输出，对首次出现的收款地址查地址表；查不到则递增 `AddressCount`、分配索引器内部 `AddressId`，建立地址与 UTXO 的内存索引，随后随基础索引批次持久化。这不是创建钱包、派生密钥或初始化 bootstrap/core 的网络地址。

该用例 `splitAssetTo` 的接收者是 `traderAActor` 和 1001 个 delegate，共 1002 个测试网 P2TR 收款地址，因此高度 2 出现 1002 个新地址。一般索引代码还处理 OP_RETURN/unknown 的内部地址表示，但本次这 1002 个新地址来自测试资金分发。

用户已批准只把 `generateAddressId` 的逐地址日志降为 Debug，代码改动一行。正常 Info 级别仍保留现有块级日志。

#### 谁在刷新 ffldb，为什么是 300 秒

这里是节点链数据库后端 `database/ffldb` 的 `dbCache`，不是 STP 插件或 Pebble 索引器。`defaultFlushSecs=300`、`defaultCacheSize=100MB` 是既有缓存默认策略：元数据先批量保存在内存，避免每个小写事务都执行磁盘元数据提交，同时用时间/大小阈值触发落盘。

300 秒不是定时后台任务。下一次可写事务执行 `cache.commitTx` 时，`needsFlush` 检查距上次刷新是否超过 300 秒或估计大小是否超过阈值；满足则当前写事务同步等待刷新。显式 `DB.Sync` 和关闭数据库也会刷新。原用例的调用路径是 `maybeAcceptBlock → DB.Update(StoreBlock) → transaction.Commit → writePendingAndCommit → cache.commitTx`。300 秒阈值不是模板合约的业务规则。

ffldb 保存原始区块文件，以及 LevelDB 元数据：区块位置/写游标、区块节点索引、主链高度/hash 映射、best state、spend journal、刷入的 UTXO、anchor 信息与合约执行状态等。原始区块文件先 Sync，再提交元数据，避免元数据引用尚未同步的区块。原用例未激活 POSV2；POSV2 的显式 Sync 属于另外一条触发路径。

已观测的约 447 毫秒停顿位于真实提交 API 内部：旧缓存 LevelDB OpenTransaction/填充/Commit 约 139/2/138 毫秒，当前事务 Commit 约 134 毫秒，区块文件 Sync 约 18 毫秒。计时日志在这些调用结束后输出，不能把这些值归因于诊断日志。LevelDB 的 API 内部还可能含 memtable 旋转、SST/manifest 写入和 compaction 等待；本轮数据没有逐一拆开这些系统调用。

#### Pebble 提交哪些索引

`IndexerMgr` 的 `compiling`（BaseIndexer）与 `contractIndexer` 共用 `baseDB`，各自构造独立 WriteBatch，最终调用共享实现 `indexer/indexer/db/pebble.go` 的 `Commit(pebble.Sync)`。以下是各批次的完整写入类别；具体一批只包含当前缓存发生变化的数据，并非每次都写所有类别。

| 批次 | 数据 | 入口 |
| --- | --- | --- |
| 基础索引 | 区块摘要；新增/删除 UTXO 及资产、UTXO ID 映射；地址→UTXO/金额及地址↔ID；ticker 信息及持有人余额；ascend/descend、channel ledger/state events、channel 信息、referrer/referrees、core node 信息；同步高度/hash/计数等统计 | `BaseIndexer.UpdateDB` |
| 合约查询索引 | `contract:v1:summary:` 合约摘要，`contract:v1:history:` 调用/执行历史记录 | `contractindex.Indexer.UpdateDB` |

此前约 815 KB 的基础批次主要受到 1002 地址资金分发的 UTXO、地址、持仓和区块数据影响；约 9 KB 为合约查询批次。没有对每类 key 做字节归因，不能声称 815 KB 包含所有上表类别。模板的权威执行快照走链数据库；DKVS 使用单独的 `dkvsDB`，不属于这两批。基础索引缓存/历史窗口的提交条件与 ffldb 的 300 秒阈值无关。

逐地址日志、模板状态编解码、ffldb 刷新与 Pebble 同步提交是分别测得的成本。降日志解决的是地址初始化的主要停顿，不代表后三类耗时已经消除。

### 降地址日志后的定向验证与模板分段（2026-10-10）

由 GPT 6 Luna Max 只运行原有 `TestNetworkTemplateAutopayCloseAcrossOutputLimit`，通过正常入口重新构建节点和 wallet 插件，仍使用 bootstrap/core、fake L1、1001 次真实签名注资及原关闭退款断言。没有增加诊断等待，没有延长 ACK，没有运行其他 case。

结果：唯一叶子 PASS 506.23 秒，包 510.624 秒，注资 1001/1001，stderr 为空。耗时含重新构建；不能直接与复用 binary 的上轮总耗时作 A/B 比较。此轮属于重复验证，不增加新增 case 计数。

#### 地址初始化已验证

| 高度 2 的 1002 个新地址 | core | bootstrap |
| --- | --- | --- |
| 地址预取全程 | 4.598 毫秒 | 4.461 毫秒 |
| DB 查询累计 | 0.622 毫秒 | 0.751 毫秒 |
| 1002 次关闭输出的 Debug 调用累计 | 0.260 毫秒 | 0.173 毫秒 |
| 实际逐地址输出数 | 0 | 0 |

core 同一区块正式校验 18.687 毫秒、入链 117.661 毫秒。此前同类地址预取曾达 1322.499 毫秒，其中日志调用 1309.349 毫秒；本轮直接验证 Info 级别下逐地址日志已消除，秒级地址预取停顿未再出现。不同运行的负载、调度及 profiler 有差异，数字不代表固定加速倍数。

#### 模板重验仍未达到 1 秒目标

core 57 个入链样本中，正式校验中位 764.462 毫秒、P95 1566.250 毫秒、最大 2023.228 毫秒（11 个超过 1 秒）；入链中位 829.920 毫秒、P95 1634.944 毫秒、最大 2054.453 毫秒（16 个超过 1 秒）。正式校验累计 39.401 秒、入链累计 43.762 秒，约占 90.0%。bootstrap 正式校验最大 1871.958 毫秒，仍有 12 次 ACK 超时，最终业务断言通过不等于性能门禁通过。

core 最慢正式入链区块为高度 17，hash `9b9e0b37b44ccf7e4918633b5deb175725ee745861b93a083c4de703797537e2`：

| 阶段 | 墙钟耗时 |
| --- | --- |
| 正式连接校验全程 | 2023.228 毫秒 |
| 合约协调器 | 1870.778 毫秒 |
| 其中模板逐笔交易执行 | 1642.822 毫秒 |
| 其中模板 Finalize | 227.092 毫秒 |
| Finalize 内：业务结算 | 9.169 毫秒 |
| Finalize 内：结果补全 / 管理余额应用 | 106.194 / 5.337 毫秒 |
| Finalize 内：已完成项清理 / 状态根及结果复制 | 81.786 / 23.987 毫秒 |
| 模块准备 | 10.313 毫秒 |
| 执行后状态根检查 | 140.136 毫秒 |

父子行不可相加。分段批量输出日志，避免逐条阶段日志影响同一测量；Finalize 自身日志输出仍包含在外层 Finalize 墙钟中，因此子段与外段存在小差值。core 提案与正式验证共 110 次模板 ExecuteBlock：逐笔执行累计 58.544 秒、全程 68.911 秒，约占 85.0%；业务结算、结果交易组装和活动探测未成为主要阶段。个别阶段有 GC/调度尾部，不能用某一个峰值推导固定每 delegate 成本。

#### 当前 CPU profile 与最小下一步方案（未实施）

core profile 有效采样 64.61 CPU 秒；模板 `loadRuntimeState` 累计 27.25 CPU 秒，全节点 GC drain 累计 17.19 CPU 秒。两者是不同调用栈统计，不能简单相加后当成模板墙钟。按 `netsync.(*SyncManager).blockHandler` 过滤，正式入链的合约校验为 19.10 CPU 秒，整个入链链路的 `loadRuntimeState` 为 13.71 CPU 秒。后续再限定到合约校验调用栈，解码为 13.43 CPU 秒，占合约校验约 70.3%；另外 0.28 秒位于合约校验之外，不能混入该比例。整份状态解码仍为主热点。

每次默认注资的框架 Lifecycle 与模板执行仍各读一次完整状态，状态内含整份 Autopay delegate map 及 Decimal 对象；保存状态会重新编码整份表，结算、关闭检查及状态根也会读状态。当前 `loadRuntimeState → json.Unmarshal(&TemplateRuntimeState) → TemplateRuntimeState.UnmarshalJSON → json.Unmarshal(alias)` 额外走了一层标准解码包装；Go 标准实现会在外层做合法性扫描，并为了调用自定义解码再次扫描到值末尾，内层又进行合法性检查和实际解码。

过滤掉 `TemplateRuntimeState.UnmarshalJSON` 内部样本之后，core 仍有 5.53 CPU 秒处于 `loadRuntimeState` 调用栈，其中外层 `json.Unmarshal` 5.31 CPU 秒。这里包含外层扫描/分发与少量其他开销，不能把 5.53 秒全部算作能消除的实际收益，也不能承诺一行修改就达到 1 秒。

**推荐下一步只改内部读取入口的一行**：`loadRuntimeState` 直接调用现有 `state.UnmarshalJSON(data)`。该方法内部仍使用标准 `json.Unmarshal` 解码 alias，并保留 InvokeItem legacy 字段拒绝、订单类型/参数一致性与 Decimal 校验。复用现有解码器，减少外层重复扫描，不增加缓存、接口、持久状态或生命周期机制，保留 JSON 格式和状态根规则。此方案尚未获得批准，未修改代码。

若批准，先补“标准入口与内部入口的状态/错误等价”定向回归（正常状态、非法 JSON、legacy 参数、类型/参数不一致、Decimal 错误、null/空状态），再由 Luna Max 只跑新增回归及这条原有批量用例，检查性能和实际 ACK 超时。整份 delegate 表的逐笔编解码仍是后续成本；更大范围的块内状态复用需另行说明复杂性并审核。

证据位于 `/private/tmp/satoshinet-template-e2e-20261009/`：`run-template-refinement.jsonl/.stderr`、`template-refinement-TestNetworkTemplateAutopayCloseAcrossOutputLimit-{core,bootstrap}.log`、`template-refinement-{core,bootstrap}.pprof`、`template-refinement-stage-summary.json`、两个 cumulative 报告、core insertion/decoder-outer 报告。本轮六个临时诊断文件已逐字节恢复；原 case、构建入口与参数已恢复，没有留下 CPU profile 参数或计时代码。新增生产改动仅逐地址 Info→Debug；既有批准修改保留，staged 为空，没有 git add/commit/push。

### 重复解码与 delegate 表成本调查（2026-10-10，优化待审核）

本轮由 GPT 6 Luna Max 运行两组短诊断；没有重跑网络批量 E2E 或全仓测试，也没有实施新的生产修复。诊断源码已归档到上述临时证据目录，仓库中的临时测试文件已移除。

#### 两种不同的重复解码

1. **单次读取内的重复扫描**：`loadRuntimeState` 的外层 `json.Unmarshal` 调用自定义 `TemplateRuntimeState.UnmarshalJSON`，后者再用标准解码器解码 alias。直接调用现有 `state.UnmarshalJSON(data)` 可去掉外层包装，保留内层 JSON、Decimal 和 InvokeItem 一致性校验。没有新增缓存或持久状态。
2. **多个调用之间的整表读取**：默认注资经过框架 Lifecycle 与模板业务执行，各读取一次完整状态；每次保存又重新编码完整 delegate 表。Finalize 的结算、关闭查询、清理和状态根也会读取。去掉外层包装不能消除这些跨调用读取。

已有节点 profile 中，`loadRuntimeState` 占 core 全节点采样 CPU 的 42.18%；后续通过两层过滤只看正式入链的合约校验调用栈，占该部分 CPU 的约 70.3%。delegate 余额汇总等业务调用栈合计约占全节点 CPU 的 1.04%，其中 `totalDelegateBalance` 约 0.94%。这些是 CPU 调用栈比例，不是入链墙钟占比，也不能用来直接推出 delegate 表的 JSON 成本。

#### delegate 表编解码的定向测量

复用已有 Autopay 运行时夹具，比较 20、500、1001 个 delegate，分别带 0、20 个待处理项。20 个待处理项通过真实 `ApplyDefaultInvoke` 入口生成；其余 delegate 为同长度诊断地址和合法 Decimal 状态。移除表的对照保留其他元数据及待处理项；完整状态先验证 JSON 往返相等。

Luna 执行：`go test -json -count=2 -timeout=8m -benchtime=150ms ./contract/template -run '^TestDelegateCostDiagnostic$'`。两轮均 PASS，用例分别 11.57、11.60 秒，包 25.0 秒，stderr 为空。各组合、操作均有两次计量。下表为两次计量的中位值：

| delegate / 待处理项 | 完整状态解码 | 去掉表后解码 | 表的解码增量占比 | 完整状态编码 | 去掉表后编码 | 表的编码增量占比 |
| --- | --- | --- | --- | --- | --- | --- |
| 500 / 20 | 6.647 ms | 0.957 ms | 85.6% | 3.822 ms | 0.179 ms | 95.3% |
| 1001 / 20 | 14.930 ms | 0.928 ms | 93.8% | 8.262 ms | 0.184 ms | 97.8% |
| 1001 / 0 | 12.401 ms | 0.012 ms | 99.9% | 9.078 ms | 0.005 ms | 99.9% |

增量占比按 `(完整状态耗时 - 去掉表后耗时) / 完整状态耗时` 计算，是这个夹具下移除字段的边际成本估计；**不能将 93.8%/97.8% 称为 delegate 占整个入链耗时的比例**。短测量受 GC、调度和样本长度影响，20 / 20 的标准解码对照有反向波动，不据此推导小表收益。主要结论以 500、1001 大表为依据。

1001 / 20 的完整 JSON 为 199865 字节，delegate 表自身为 192633 字节。一次标准完整解码分配约 1.44 MB、24862 次；完整编码分配约 1.68 MB、24379 次。余额求和约 0.841 ms。热点主要来自整表 JSON 与 Decimal 对象的反复分配。

对同一份状态，直接调用现有 `UnmarshalJSON` 的完整解码中位 11.758 ms，较标准包装入口的 14.930 ms 减少约 21.2%；两轮分别减少约 18.1%、23.8%。这个比较保留 `GetState` 的防御性字节复制。

#### 保持 JSON 字节不变的 Decimal 编码优化原型

`indexer/common/decimal.go` 的 `Decimal.MarshalJSON` 为每个金额创建 `map[string]interface{}`，再走 map 排序、反射及 interface 编码。可改成只有 `Precision`、`Value` 两个字段的固定结构体，保留 `Validate()`、原始整数文本和字段顺序。此处是共享基础包，生产修改及其回归范围需要审核。

临时原型仅替换 delegate 表中 Decimal 的编码，保留其他元数据、InvokeItem 和表结构。1001 / 20 夹具先断言整个 JSON **逐字节一致**，再计量两种编码；这只证明该夹具，不能替代非法精度、负值、零、nil 和大整数的回归。

Luna 执行：`go test -json -count=2 -timeout=4m -benchtime=300ms ./contract/template -run '^TestDelegateEncodingDiagnostic$'`。两轮均 PASS，用例分别 1.85、1.81 秒，包 5.135 秒，stderr 为空。

| 轮次 | 原完整编码 | 替换 delegate Decimal 后完整编码 | 耗时减少 |
| --- | --- | --- | --- |
| 1 | 7.394 ms | 4.540 ms | 38.6% |
| 2 | 6.870 ms | 3.897 ms | 43.3% |

两轮中位值：分配次数由 24374 降至 10348，减少 57.5%；分配字节由约 1.58 MB 降至 0.53 MB，减少 66.7%。这里只量了完整状态编码，未量全节点或入链性能，不能保证 1 秒目标已达到。

#### 推荐实施顺序及复杂性决策

**先审核两项局部优化**：内部读取直接调用现有解码器；共享 `Decimal.MarshalJSON` 用固定结构体替代临时 map。保留校验、JSON 字节格式和状态根规则，补解码入口等价及 Decimal 编码字节/错误等价回归，由 Luna Max 仅执行新增回归和原有 1001 人网络 case。实际收益及 ACK 超时以网络复验为准。

**彻底消除跨调用整表编解码需要另行审核块内状态复用**：每个合约在一次块执行中只加载一份结构化工作状态，Lifecycle、业务执行和 Finalize 使用这份状态，在执行边界编码保存。提案执行与正式验证仍各自独立，不复用前一轮的执行结果。

这会引入工作状态的所有权与同步边界。当前 `RuntimeState()` 返回新对象，失败调用不能污染已保存状态；`RuntimeStore.Clone()` 必须继续隔离父状态与候选块；`RuntimeBase.SetState`、恢复快照和直接替换状态的入口也必须正确处理。InvokeItem 的 JSON 往返还涉及文本 Decimal 的重新解析，不能直接将序列化前对象当成解码后等价对象。需要先明确这些规则及回归范围，再改生产设计。只给 `RuntimeState` 加缓存而每笔保存仍编码/失效，不能消除主要循环；直接共享返回指针则会改变错误与隔离语义。

当前不建议拆 delegate 持久表、增加余额汇总持久字段或更改 Decimal/JSON 格式：业务求和的 profile 占比低，这些方案增加数据一致性及协议维护成本。先测两项局部优化，再根据正式入链结果决定是否承担块内状态复用的改动范围。

本轮证据：`run-delegate-cost.jsonl/.stderr`、`delegate-cost-summary.json`、`delegate_cost_diagnostic_test.go.source`、`run-delegate-encoding.jsonl/.stderr`、`delegate-encoding-summary.json`、`delegate_encoding_diagnostic_test.go.source`、`delegate-decimal-encode-profile.txt`。临时测量不作为新增 E2E 验收 case 计数，未改变全仓自动测试入口。

### 代码导航与正式入链样本归因（2026-10-10）

以下位置对应第一批三项优化前、撤下临时计时代码后的业务源码；告警清理实施后 `chain.go`、`versionbits.go` 等行号会变化，本节保留调查时的调用导航。这里描述本例实际走过的旧 POS `OnBlockGenerated` 路径；POS v2 会在该方法前段转入 `onPOSBlockGenerated`，不能直接套用本例时序。

#### 入链主调用顺序

```text
mining/posminer/validatormanager.go:557 OnBlockGenerated
  :645 Chain.CheckConnectBlockTemplate(block)        [提案校验，第一次合约执行]
  :682 cfg.ProcessBlock(block, BFFastAdd)            [入链计时入口]
    netsync/manager.go:1719 ProcessBlock             [提交 processBlockMsg，等待响应]
      :1459 blockHandler → chain.ProcessBlock
        blockchain/process.go:184 ProcessBlock      [readiness 与 chainLock]
          :206 processBlockLocked
            :300 maybeAcceptBlock
              blockchain/accept.go:50 checkBlockContext
              :70 checkConnectBlock                 [正式校验，第二次合约执行]
                blockchain/validate.go:1456 checkConnectBlockFor
                  validateContractBlockFor
                    contract/node/contract_validation.go:124 validateContractBlock
                      :150 coordinator.ValidateBlock
                        contract/framework/coordinator.go:53 module.ExecuteWorkBlock
                          contract/node/template_validation.go:71 template.ExecuteBlock
                            contract/template/backend.go:71 ExecuteBlock
                              遍历 ExecuteTx → 框架执行 → 模板业务
                              Finalize
                      :174 RecordContractBlockState → 核对状态根、保存候选快照
                      :179 moduleBlockPostState → 再核对状态根
              blockchain/accept.go:94 db.Update(dbStoreBlock)
              :107 block index flushToDB
              :115 connectBestChain
                blockchain/chain.go:1355 UTXO connectTransactions
                :1361 connectBlock
                  :643 原子 DB 事务，含最佳链、spend journal、合约状态
                  :776 assetIndexerMgr.ConnectBlock
                  :784 UTXO 条件刷新
                  :802 NTBlockConnected 通知
              blockchain/accept.go:120 finishBlockAcceptance
                POS v2 激活时另有 database.Sync；旧 POS 本例不走该条件
  mining/posminer/validatormanager.go:698 QueueMessage(MineAck)
```

`maybeAcceptBlock` 的 :55–59 注释说明正式重验理由：区块 hash 不绑定 witness，且先前保留的执行状态可能已被释放。`BFFastAdd` 不能绕过 :63–70 的合约预验证。该步骤成功后设置 `statusValid`；`connectBestChain` 在 :1309 检查 KnownValid，本例不会在 :1328 再执行第三次 `checkConnectBlock`。区块结果构建、其他节点验证及重试耗时需另行计数。

#### 一笔默认注资：两次读整份状态、一次写整份状态

```text
contract/framework/default_invoke.go:43 对每个 call 执行
  :46 checkLifecycle(call)
    framework/executor.go:127 Backend.Lifecycle
      template/managed_balance.go:35 Store.ContractClosed
        :23 RuntimeState
          template/state.go:1515 loadRuntimeState       [完整解码 #1]
  :53 Backend.DefaultInvoke
    template/backend.go:executeDefaultInvokeOutputTx
      :284 RuntimeState                                 [完整解码 #2]
      :288 checkInvocationLifecycle(state, ...)
      :297 checkAutopayDelegateCapacity(..., &state, ...)
      :319 applyDefaultInvoke(state, request)
        template/state.go:737 NewDefaultInvokeItemFromRequest
        :748 append(state.Items, item)
        :749 state.ApplyForContract → Autopay.ApplyRunningData
          template/autopay.go:380 addDelegateBalance
            :388 totalDelegateBalance                   [业务求和]
        template/state.go:750 saveRuntimeState
          :1672 json.Marshal(state)                     [完整编码 #1]
          :1676 SetState                                [复制保存 JSON 字节]
  framework/default_invoke.go:74 acceptOutcome           [管理资金数量核算]
```

前一轮批准的局部复用已经使模板业务的生命周期、容量检查与更新共用第 #2 次解码结果。剩下的第 #1 次来自通用框架 Lifecycle；它只需要关闭状态，却经 `ContractClosed` 解码全部 delegate 表。

#### 单次读取内部的两层 JSON 包装

当前 `contract/template/state.go:1658`：

```go
data, ok := r.GetState(runtimeStateKey)
// 处理缺省/空状态后：
var state TemplateRuntimeState
if err := json.Unmarshal(data, &state); err != nil {
    return TemplateRuntimeState{}, err
}
```

这会调用 `contract/template/state_consistency.go:10`：

```go
type rawTemplateRuntimeState TemplateRuntimeState
var decoded rawTemplateRuntimeState
if err := json.Unmarshal(data, &decoded); err != nil {
    return err
}
// 逐项 validateInvokeItemParamConsistency，通过后才赋值到接收者。
```

拟审核的局部修改仅将第一段的 `json.Unmarshal(data, &state)` 改成 `state.UnmarshalJSON(data)`，第二段保持不变。alias 没有同名解码方法，因此不会递归。`RuntimeBase.GetState` 在 `runtime.go:241` 返回字节副本；直接调用不会取消该复制。

这里的“两层”不表示同一次读取构建了两份完整状态对象：Go JSON 的外层会校验 JSON、扫描并跳过值，再调用自定义方法；内层 alias 解码才构建状态对象。直接调用可以去掉外层重复扫描，但不能消除每笔 Lifecycle 与业务入口各自整表解码。

Decimal 编码热点位于共享仓库 `indexer/common/decimal.go:222`：当前校验后 `json.Marshal(map[string]interface{}{"Precision": d.Precision, "Value": d.Value.String()})`。拟改为同字段顺序的固定结构体，与 delegate 业务求和是两个不同位置；未实施。

#### Finalize 与状态根仍会整表读写

按 `template/backend.go:355 Finalize` 顺序：

| 顺序 | 入口 | 触发完整状态读取/编码的位置 |
| --- | --- | --- |
| 1 | :359 SettleBlockWithGasConfigAndPrecision | Autopay 分支 `runtime.go:160` 读取，:168 保存 |
| 2 | :363 构建结果计划、:380 补记录 | 结果计划及余额绑定，本例不是主要 CPU 热点 |
| 3 | :385 AugmentResultPlans | `result.go:199` 经 ContractClosed 读取 |
| 4 | :389 ApplyManagedResultBalances | 使用 Store.ContractClosed，仍读取 |
| 5 | :392 PruneFinishedItems | `state_root.go:102` 读取，清理后 :111 保存 |
| 6 | :397 Store.StateRoot | `state_root.go:33` 读取，:47 编码 canonical payload |

状态根还有块外层调用：`template_validation.go:66` 计算父根，:98 保存候选状态前校验根，:152 返回候选状态时再次计算根；复合校验器 :183 读取上述返回根作比较。canonical payload 与运行状态 JSON 不完全相同，不能用原始 JSON 字节直接代替根计算。

#### 只对齐正式入链，不混入提案的墙钟归因

重新解析降地址日志那一轮已有日志：以区块 hash 的 `FORMAL-DIAG` 匹配紧邻的 VALIDATE/EXEC/FINAL，再核对高度一致。57 个入链区块中 55 个有模板 ExecuteBlock；总入链 43.761883 秒、正式校验 39.400535 秒，提案校验额外 37.115392 秒，不在下表分母内。

| 互不重叠的阶段 | 累计墙钟 | 占 43.761883 秒入链 |
| --- | --- | --- |
| 模板逐笔 ExecuteTx | 29.960447 秒 | 68.46% |
| 模板 Finalize | 5.393886 秒 | 12.33% |
| 模块准备，含父状态根 | 1.257990 秒 | 2.87% |
| 执行后状态记录/状态根检查 | 1.994160 秒 | 4.56% |
| 协调器除模板执行之外 | 0.112241 秒 | 0.26% |
| 正式校验其余，含通用校验等差额 | 0.681812 秒 | 1.56% |
| 正式校验之外的入链余量 | 4.361348 秒 | 9.97% |

各行按边界相减，合计整个入链，避免父子计时重复相加。Finalize 子段日志输出在外层耗时中，不能把表内比例视为无诊断开销的稳定性能常数。整个测试数百秒还包含构建、钱包注资、出块间隔、提案校验及同步等待；不属于这 43.76 秒。

正式入链调用栈 CPU profile：`ProcessBlock` 20.25 CPU 秒、合约校验 19.10 CPU 秒。先过滤 blockHandler 并保存过滤后的 profile，再过滤 `validateContractBlockFor`，得到合约校验内 `loadRuntimeState` 13.43（70.3%）、`saveRuntimeState` 3.90（20.4%）；两项合计约占合约 CPU 的 90.7%。仅过滤 blockHandler 时 load 为 13.71，另有 0.28 位于校验外的状态读取，前文曾将该不同边界除以 19.10 得到 71.8%，现已修正。不能再加内部的 JSON、Decimal、malloc CPU；这些内部成本已包括在方法累计中。整个入链状态根累计 2.29 CPU 秒也与上述读取/编码有重叠。全节点后台 GC drain 17.19 CPU 秒不在正式执行调用栈内，不能据此把入链墙钟的差额全部归为 GC。

校验之外的最大余量还未定位：高度 49 hash `9a4546107d985fc50ca66bedc266f59e873a23c1df654ba2cbb91e7e305bd6df` 入链 1572.199 ms、正式校验 974.337 ms，余量 597.861 ms；高度 39 hash `3d30de2ec44261d73adb60481677d497723fe5bc199ef8f8e05f74edb43f617a` 余量 506.084 ms。现有这一轮没有细分 DB/索引计时，不能直接归给 ffldb、Pebble 或日志。

本轮解析产物位于临时证据目录：`analyze-formal-aligned.py`、`formal-aligned-summary.json`、`formal-aligned-blocks.json`、`formal-cpu-all.txt`、`formal-only.pb.gz`、`formal-contract-cpu.txt`、`formal-cpu-excluding-state-io.txt`。生产编解码优化仍待审核。

#### 新一轮深层观察：落库、索引和日志边界

由 GPT 6 Luna Max 仅执行 `TestNetworkTemplateAutopayCloseAcrossOutputLimit`，PASS 635.81 秒、包耗时 638.133 秒，1001/1001 注资，stderr 为空。本轮额外采集 ffldb、Pebble、队列和锁的临时计时；12 份诊断源码已归档，11 个既有文件逐字节恢复，新增 helper 已删除，未修改 ACK 上限或生产编解码逻辑。

core 68 个入链样本累计 84.754 秒，正式校验 30.844 秒、原始块 DB 10.857 秒、accept block-index 刷新 2.527 秒、connectBestChain 34.665 秒，另有边界输出等余量。中位 1.163 秒、P95 2.325 秒、最大 2.856 秒；bootstrap 记录 30 次 ACK 超时。因此功能通过不表示满足 1 秒准出要求。

**这一轮不能作为新的无干扰耗时占比结论**：深层日志是同步输出，且本轮与上一轮区块数量不同，未做受控 A/B。68 个原始块 DB 外层累计 10.857 秒，内部 DB total 累计 8.575 秒，差额 2.282 秒经过诊断输出、返回和调度边界；部分单块差额达到 40–97ms。CPU profile 中正式入链的日志输出累计 0.89 CPU 秒，但 CPU profile 不包含全部 IO/锁等待墙钟，不能据此扣除全部日志干扰。

本轮确定的具体慢点及仍需细分的边界：

| 样本/阶段 | 观察 | 代码与解释 |
| --- | --- | --- |
| hash `1ffdad1247b9844a25db58a3a8ac9e75e0a5a307e98c76d3d3d1ccb26b102ba5` | 入链 2.856 秒；raw DB 2.049 秒；内部 commit 2.030 秒 | `ffldb/dbcache.go:567 commitTx` 命中刷新，`:488 flush` 先同步块文件，再提交缓存 treap，最后提交当前事务 treap |
| 同一 ffldb 样本内部 | 块文件 Sync 55.639ms；缓存 LevelDB OpenTransaction 489.121ms、Commit 999.850ms；当前事务 Commit 290.959ms | `dbcache.go:423 updateDB`，主要尖峰在 LevelDB 开事务/提交边界，不能称为块文件 Sync 慢 2 秒。底层 OpenTransaction 会轮换 memdb、条件等待 compaction；Commit 会生成表、提交 manifest、条件等待 compaction，尚未细分这些内部等待 |
| hash `17c6f5f79fd2f2c74b99c7c5a774f2b9e97e41e8417bb961e897283c2f8c7eb8`，高度 24 | `connect_index_flush` 1.297 秒，但接入前索引 DB total 46.276µs；随后原子链状态事务 8.330ms | 临时标签覆盖 `chain.go:599 connectBlock` 开始到 :625 flush 返回，**包含 :615–619 的规则告警**，不能将整个 1.297 秒归为 block-index IO。该区块连续输出 29 条 `Unknown new rules are about to activate` |
| 相邻高度 25 | 同一前段 1.251 秒，连续 29 条同类 Warn，索引 DB total 91.811µs | `versionbits.go:299 warnUnknownRuleActivations` 循环各 bit，:321 每个 LockedIn bit 写 Warn；profile 该路径采样落在日志 Write syscall。日志/调度等待是强嫌疑，精确墙钟仍需单独测该函数及日志写入 |
| base Pebble 批次 | 同步到高度 39 时，持久化滞后快照高度 19，815229 字节 Commit 874.016ms；base Flush 893.569ms | 与 ffldb 的 LevelDB 是不同数据库；之后第二批 9234 字节 Commit 171.812ms。下一次快照高度 39 的 395178 字节 Commit 71.476ms，不能用单次峰值作固定成本 |
| netsync 队列与 chainLock | core 队列最大 0.887ms，锁等待最大约 1µs | 这次 core 慢入链不是这两个边界的排队造成；不能推广到其他负载/节点 |

新 profile 仍验证合约 CPU 热点：正式合约校验 19.88 CPU 秒，范围内 loadRuntimeState 13.99（70.4%）、saveRuntimeState 3.76（18.9%），合计 89.3%。它支持优先处理整表编解码，不支持将 ffldb、Pebble、日志尖峰忽略。

下轮观察应减少深层逐事务日志，在内存汇总并于测量结束输出；单独计时规则告警/日志写入，以及 LevelDB 的 memdb 轮换、表生成、manifest 提交和 compaction 等待。保持正式重验、状态根校验和数据持久化保证；未批准前不更改刷盘周期或引入后台写入机制。

证据目录：`/private/tmp/satoshinet-template-e2e-20261009/tail-observation/`，包含 `run.jsonl/.stderr`、两个节点日志和 CPU profile、`stage-summary.json`、`cpu-formal-contract.txt` 与 `instrumented/`。首次诊断编译因日志使用不存在的 DB 方法失败，已归档为 `run-build-failure.*`，仅更正临时日志后重跑通过。

### 入链优化方案与规则告警删除范围（2026-10-10，第一批已批准）

本节保留用户审核的方案与优化前代码位置。用户已批准第一批三项并要求 GPT 6 Luna Max 只运行相关回归与原有 1001 人网络 E2E；第二、第三批生产设计仍待另行审核。用户确认聪网的规则跟随比特币规则演进；本次删除范围限于聪网本地未知版本位告警，实际规则启用和交易校验仍由现有实现负责。

#### 29 条规则告警的原因及删除建议

`chaincfg.TestNetParams` 在 `chaincfg/params.go:1052` 设置 `RuleChangeActivationThreshold=0`、窗口 10。告警专用 `bitConditionChecker` 直接使用这个阈值，且其 HasStarted 恒真、HasEnded 恒假。`blockchain/thresholdstate.go:225` 使用 `count >= threshold`，因此某个位即使零票，也会从 Started 进入 LockedIn。告警遍历 29 个 bit，造成高度 20–29 窗口逐块输出 29 条告警；这与本轮高度 24/25 的日志相符。

该函数查看的是聪网本地区块版本及本地参数，不查询 BTC L1；它不承担比特币规则同步或脚本验证。不能通过提高公共激活阈值来消除日志，因为该参数还参与实际 deployment 激活。

建议完整删除告警专用路径：

1. 删除 `blockchain/chain.go:615–622` 的 `warnUnknownRuleActivations` 调用。
2. 删除 `blockchain/thresholdstate.go:416–423` 的 warning cache 预热，以及 :434–443 的启动告警调用；保留 :424 的已知 deployment 初始化。
3. 清理告警函数、`bitConditionChecker`、`warningCaches`、`unknownRulesWarned` 和仅为这些符号服务的初始化/测试夹具字段，避免遗留死代码。
4. 保留 `deploymentChecker`、`deploymentCaches`、`deploymentState`、`CalcNextBlockVersion` 和 `validate.go` 中 CSV、SegWit、Taproot 等实际校验。

删除后不再根据聪网本地 version bits 提醒“未知软分叉”；在用户确认的规则演进策略下接受这一取舍。当前实际规则仍通过本地部署参数与节点代码启用，不能将删除告警描述为已经实现自动跟随 BTC L1 新规则。

#### 建议实施顺序

| 批次 | 改动 | 收益证据与验收 |
| --- | --- | --- |
| 第一批 A | 删除上述告警专用路径 | 去掉 29 位告警计算及同步日志；原计时 1.297 秒包含其他前段操作，实际节省由复验确认。跨越原 LockedIn 窗口，核对无此类误告警、正常入链且已知规则校验仍生效 |
| 第一批 B | `template/state.go:1664` 直接调用现有 `state.UnmarshalJSON(data)` | 1001/20 夹具完整解码减少约 21.2%；保留所有解码一致性检查，比较正常/非法状态的结果与拒绝行为 |
| 第一批 C | 共享 `indexer/common/decimal.go:222` 用固定结构体代替临时 map | 诊断原型的完整状态编码减少 38.6–43.3%；保持字段顺序、JSON 字节和 Validate 行为，检查零、负数、大整数、非法精度及 nil 路径 |
| 第二批，单独设计 | 每个候选块内每个合约使用一份结构化工作状态，Lifecycle、业务和 Finalize 复用，明确边界后再编码 | 针对仍占合约 CPU 约 90% 的状态读写；不能承诺第一批就消除逐笔整表成本。需明确失败调用隔离、候选块 Clone、InvokeItem 规范化、状态根与快照往返，保持提案和正式验证独立执行 |
| 第三批，持久化专项 | ffldb 同次刷新把旧缓存及当前事务的元数据合并为一次提交 | 当前为两次 OpenTransaction/Commit；保留块文件先 Sync、旧值先应用/新值后覆盖、成功才清缓存。需要重叠 Put/Delete、提交失败、重试、重启恢复回归，单独审核失败中间状态；可能减少一次提交，仍需定位剩余 LevelDB 内部等待 |
| 并行研究项 | Pebble 的 WAL Sync、写阻塞及 compaction 等待做低输出计时 | 当前峰值变化较大，尚不足以指定参数修改。基础索引和合约索引的分开提交有现有失败恢复约定，合并会改变提交边界，应另行给出方案，不能仅为减少一次 Sync 直接合并 |

第一批复验由 GPT 6 Luna Max 执行，只覆盖相关回归与现有 1001 人网络 case。通过正常构建入口重建节点和插件；记录提案/正式验证/入链的 P50、P95、最大值和 ACK 超时，区分功能通过与 1 秒目标。本轮沿用较少日志输出的阶段计时和 CPU profile，与此前同口径记录比较；临时计时于结束后恢复。

现阶段不建议增加 delegate 汇总持久字段或拆分持久表，业务求和并非主要热点；不建议通过改为 NoSync、后台落库、延长刷盘周期或延长 ACK 来解决这些已定位的成本。


### 第一批三项优化实施与定向复验（2026-10-10）

已按用户批准完成三项生产改动：删除未知版本位告警专用调用、检查器及缓存，保留已知 deployment 和实际共识校验；`ContractRuntime.loadRuntimeState` 直接调用现有 `TemplateRuntimeState.UnmarshalJSON`；共享 `indexer/common.Decimal.MarshalJSON` 用固定结构体替代临时 map，保持 Precision/Value 字段顺序、字节及 Validate 行为。本批生产差异已单独归档为 `phase1-optimization/production-this-batch.patch`，避免把原有未提交变更算入本批。

回归由 GPT 6 Luna Max 执行，只运行以下 10 个顶层 case，全部 PASS，三个包的 stderr 均为空：

| 文件/包 | 定向用例 | 验证边界 |
| --- | --- | --- |
| `blockchain/deployment_warning_regression_test.go`、`blockchain/thresholdstate_test.go` | `TestKnownDeploymentsWithoutUnknownRuleWarnings`、`TestThresholdStateTransition` | 前者在修改前于高度 20 复现 29 条告警；修改后跨 Defined/Started/LockedIn/Active，检查已知部署状态与下一块版本，无未知规则告警 |
| `contract/template/runtime_decode_regression_test.go` | `TestRuntimeDecodePreservesValidationAndState` | 1001 delegate 状态、正常/空/null/未知字段、非法 JSON 与尾部数据、旧 item 格式、参数和 Decimal 非法值；与标准 json.Unmarshal 比较结果及错误类型，检查读取不修改原字节和状态隔离 |
| `contract/template` 既有定向回归 | `TestRuntimeStoreMarshalRoundTrip`、`TestDecodeRuntimeStoreValidatesInnerTemplateState`、`TestDefaultInvokeLoadedStateMatchesRuntimePath`、`TestDefaultInvokeStateReuseSequentialOutputsAndCapacity`、`TestDefaultInvokeStateReuseFailurePreservesState` | 状态往返、内层格式拒绝、已有默认调用复用的一致性、连续输出/容量及失败状态保持 |
| `indexer/common/decimal_json_regression_test.go` 与既有 common 回归 | `TestDecimalJSONPreservesWireBytesAndValidation`、`TestDecimalRejectsInvalidProtocolPrecision` | 与旧 map 编码逐字节相同，零/负数/大整数/精度上限、嵌套与往返、nil 和非法值仍按现有规则拒绝 |

随后同一 Luna Max 仅执行原有网络 `TestNetworkTemplateAutopayCloseAcrossOutputLimit`，通过正常入口重新构建节点和 wallet 插件。唯一用例 PASS 450.60 秒，包耗时 454.712 秒，完整 JSONL 无 fail，stderr 0 字节；1001/1001 真实签名注资，原有分块退款、每人完整本金且不重复、Result 输出上限、关闭状态和双节点同步断言全部通过。没有运行全仓测试。

#### 相同计时边界下的前后结果

基线采用之前降地址日志后的低输出测量（用例 506.23 秒），不用额外深层日志导致 635.81 秒的那轮。基线正式入链样本 57 个，本轮 58 个，下面比较分位数而非直接比较累计总量；这是同一用例的单次前后观察，仍受运行负载、区块分组、CPU profile 和阶段日志影响，不能将收益解释为稳定保证或三项各自独立收益。

| 指标 | 优化前 | 本轮 | 降幅 |
| --- | --- | --- | --- |
| core 正式校验 P50 / P95 / 最大 | 764 / 1566 / 2023 ms | 451 / 1256 / 1846 ms | 41.0% / 19.8% / 8.8% |
| core 入链 P50 / P95 / 最大 | 830 / 1635 / 2054 ms | 546 / 1368 / 1943 ms | 34.2% / 16.3% / 5.4% |
| bootstrap 正式校验 P50 / P95 / 最大 | 718 / 1452 / 1872 ms | 438 / 1320 / 1366 ms | 38.9% / 9.1% / 27.0% |
| core 正式校验超过 1 秒 | 11/57 | 4/58 | — |
| core 入链超过 1 秒 | 16/57 | 11/58 | — |
| bootstrap 正式校验超过 1 秒 | 11/57 | 6/58 | — |
| bootstrap ACK 超时，按 PEER 超时事件去重 | 12 次 | 9 次 | 25.0% |
| core ACK 超时 | 0 次 | 0 次 | — |
| 未知规则告警，每个节点 | 291 条 | 0 条 | 全部消除 |
| E2E 用例总耗时，包含构建与等待 | 506.23 秒 | 450.60 秒 | 11.0% |

功能通过，性能改善，但仍未达到“入链控制在 1 秒内、ACK 不超时”的目标。ACK 上限仍为原有 2/4 秒，没有延长等待或删减校验。

#### 本轮剩余 ACK 超时与热点

9 次 bootstrap PEER 超时均保留计数，未把一条超时派生的多条 MINR 错误重复算入。前两次在 22:48:55/57 收到提案，而 core POS miner 于 22:48:58 才启动；`server.go:1550 OnMineBlock → posminer.go:495 OnBlockGenerated` 在 `!m.started` 时直接返回，没有进入 ValidatorManager 校验与 ACK 路径。这两次属于启动时序，不是慢入链样本。

其余 7 次按区块 hash 对齐到 core 的提案校验与入链。`validatormanager.go` 顺序仍是 CheckConnectBlockTemplate → ProcessBlock → QueueMessage(MineAck)。提案计时与入链计时边界不重叠，因此即使单次正式校验小于 1 秒，两段相加仍可超过 2 秒：

| 高度 | 提案校验 | 正式入链，含正式校验 | 两段合计 |
| --- | --- | --- | --- |
| 39 | 1095 ms | 1482 ms | 2577 ms |
| 44 | 780 ms | 1943 ms | 2723 ms |
| 46 | 1086 ms | 1016 ms | 2102 ms |
| 49 | 987 ms | 1022 ms | 2009 ms |
| 50 | 1071 ms | 1298 ms | 2369 ms |
| 51 | 871 ms | 1267 ms | 2139 ms |
| 54 | 914 ms | 1368 ms | 2282 ms |

先按 netsync blockHandler 过滤 CPU profile，再在过滤后的 profile 中限定 validateContractBlockFor，正式合约校验累计 16.64 CPU 秒：loadRuntimeState 12.36（74.3%）、saveRuntimeState 2.57（15.4%），合计 89.7%。这里按合约函数累计 CPU 为分母，未使用 pprof 全节点总量百分比，也不把内部 JSON/Decimal/分配再次相加。

按 hash/高度对齐正式入链的互不重叠墙钟：58 块总入链 35.254 秒；模板逐笔 ExecuteTx 22.558 秒（64.0%），Finalize 3.937 秒（11.2%），模块准备 1.157 秒（3.3%），执行后状态根/记录 1.252 秒（3.6%），协调器其他 0.215 秒（0.6%），正式校验其他 0.733 秒（2.1%），校验外入链余量 5.402 秒（15.3%）。提案校验额外 28.133 秒，不包含在 35.254 秒内。校验外最大单块余量 542.657ms，本轮没有 ffldb/Pebble 内部细分，不能将余量全部归给数据库或日志。

建议下一批仍优先按前节方案处理候选块内结构化状态复用，保留提案与正式验证独立执行以及失败隔离；该设计尚未实施，需用户单独审核。启动时序和 ffldb/Pebble 持久化尖峰作为独立问题保留，不能仅凭本轮数据更改持久化边界或 ACK 上限。

证据：`/private/tmp/satoshinet-template-e2e-20261009/phase1-optimization/` 中的 `*-after.jsonl/.stderr`、两节点 `phase1-*.log/.pprof`、`comparison.json`、`phase1-stage-summary.json`、`formal-aligned-summary.json`、`formal-aligned-blocks.json`、`formal-contract-cpu.txt`、`ack-timeout-aligned.json` 和 `instrumented/`。六份临时诊断源码已归档并逐字节恢复；三项生产差异恢复后与本批 patch 完全一致，已批准的地址 Debug 保留，两仓 git diff --check 通过，staged 均为空，未 add/commit/push。


### 第一批之后的性能继续定位（2026-10-10）

本次复用上一节已通过的网络 E2E 日志与 CPU profile，未增加测试轮次，未修改生产代码。对六份计时恢复后的源码逐字节比对，均与该次测量所用生产源码一致。新的分析产物保存在 `/private/tmp/satoshinet-template-e2e-20261009/phase1-followup/`。

#### 将 89.7% 状态读写成本拆到调用来源

从原始 core profile 先限定 netsync blockHandler，再限定 validateContractBlockFor，保存 `contract-only.pb.gz`。此时分母为正式合约校验 16.64 CPU 秒，以下各行互不重叠：

| 调用来源 | CPU 秒 | 占正式合约校验 CPU |
| --- | --- | --- |
| 框架 Lifecycle 为关闭检查解码整份状态 | 4.77 | 28.67% |
| 默认注资业务再次解码整份状态 | 5.05 | 30.35% |
| 状态根、恢复、结算、清理及其他关闭查询的解码 | 2.54 | 15.26% |
| 保存状态时整份编码 | 2.57 | 15.44% |
| 其余工作 | 1.71 | 10.28% |

完整默认调用顺序：

```text
framework/default_invoke.go:46  Executor.checkLifecycle(call)
  framework/executor.go:127  Backend.Lifecycle(call.Contract)
    template/managed_balance.go:35  Store.ContractClosed
      template/managed_balance.go:23  RuntimeState
        template/state.go:1658  loadRuntimeState（整表解码 #1）
framework/default_invoke.go:53  Backend.DefaultInvoke
  template/backend.go:284  RuntimeState（整表解码 #2）
  template/backend.go:288  checkInvocationLifecycle（复用 #2，无第三次读取）
  template/backend.go:297  checkAutopayDelegateCapacity（复用 #2）
  template/backend.go:319  applyDefaultInvoke（复用 #2）
    template/state.go:750  saveRuntimeState（整表编码）
```

第一个读取不是磁盘随机查询；`loadRuntimeState` 先从 `RuntimeBase.state` 的内存字节中复制，再构造完整的 map、delegate 和 Decimal 对象。该轮 GetState 的复制累计 0.12 CPU 秒，约占 0.72%，优先去掉防御性字节复制收益有限。1000 人附近一个块若有 20 笔默认注资，一轮验证约会遍历 4 万个 delegate 做解码、2 万个做编码；提案与正式验证还各自执行一轮。这是根据当前循环推导的工作量，不是新的实测计数。

Decimal.UnmarshalJSON 累计 5.66 CPU 秒（34.01%），包含在上述解码行内；每个金额重新进行 JSON 对象解析与十进制 big.Int 构造。InvokeItem.UnmarshalJSON 的 legacy 字段扫描与正式字段解码累计 0.65 CPU 秒（3.91%）。当前两者都存在成本，但优先减少整份状态的读取次数，可同时减少这些内层操作，无需先编写专用 JSON 解析器。

Autopay 默认注资的 `totalDelegateBalance` 累计 0.52 CPU 秒（3.12%），已包含在“其余工作”中。它确实每次遍历 delegate 求和，但当前主要瓶颈是整张表的编解码，不宜先增加汇总字段或改变持久化结构。

#### 慢区块与剩余等待边界

| 样本 | 已定位到的墙钟阶段 | 结论 |
| --- | --- | --- |
| 高度 44，`dec16e005061…` | 入链 1943.393ms；正式校验 1845.983ms；其中模板逐笔 ExecuteTx 1668.334ms，Finalize 118.062ms；校验外 97.410ms | 本轮最大入链样本的主段在模板执行。相同块提案 ExecuteTx 为 583.456ms，正式重验放大约 2.86 倍；现有 CPU profile 没有逐块时间标签，不能进一步断言额外等待全部是 GC |
| 高度 39，`c61e35f43f28…` | 入链 1482.412ms；正式校验 939.754ms；校验外 542.657ms | 同期日志明确经过 `performUpdateDBInBuffer → updateBasicDB 19`，与周期索引提交重合。但缺少内层计时，542.657ms 不能全部标为 Pebble Commit |
| 高度 57，`a2973985a06d…` | 入链 731.915ms；正式校验 314.866ms；校验外 417.049ms；地址预取 4.205ms，服务快照 Clone 15.905ms | 不在 base 索引快照提交点。该时刻距离 ffldb 初始化约 303 秒，与 300 秒刷新阈值相符；这是待验证关联，不能仅靠时间接近认定发生了 ffldb 刷新 |

`ProcessBlock - 正式校验` 的余量覆盖原始块 DB、block-index、链状态事务、同步资产索引、最后 UTXO 刷新和通知等多个边界。代码顺序为 `accept.go:94 db.Update(dbStoreBlock) → :107 index.flushToDB → :115 connectBestChain → chain.go:619 链状态事务 → :753 assetIndexerMgr.ConnectBlock → :761 UTXO flush`；具体行号以当前源码为准。资产索引内 `indexermgr.go:513 performUpdateDBInBuffer → base_indexer.go:344 CommitBackup → backup.UpdateDB → base_indexer.go:748 wb.Flush → indexer/indexer/db/pebble.go:781 Commit(pebble.Sync)` 同步返回后才继续入链。

全节点 GC drain 为 14.49 CPU 秒，其中 idle worker 9.15、dedicated worker 5.34；这些发生在其他运行时调用栈，不能与正式校验的 16.64 相加当成墙钟，更不能把 idle GC CPU 全部算成阻塞业务。反复分配会增加 GC 工作的判断有依据，具体慢块的 GC 暂停、调度和 IO 等待仍需要 trace 或带块标识的墙钟计时。日志 Entry.write 全节点累计 0.73 CPU 秒同样不包含全部锁/IO 等待，不能据此说日志完全没有影响。

#### 下一步推荐的更小改动范围，待审核

先尝试**一次默认调用内复用解码结果**，暂不扩展为跨交易的完整块缓存：在 `Backend.ExecuteTx` 的当前执行作用域中，Lifecycle 完整解码并校验的状态可由紧随其后的、同一合约的 DefaultInvoke 一次性消费；成功后继续原来的 saveRuntimeState，每个后续输出仍重新走 Lifecycle；错误、拒绝、作用域结束均清理，直接调用 DefaultInvoke 时保留正常读取路径。这样无需新增持久字段，也不改变通用框架的生命周期准入职责，优化目标是表内约 28.7% 的当前 CPU 成本，实际节省仍需测量。

该方案仍增加一份短期对象及其清理规则，属于需要用户审核的生产设计；尚未实施。回归应覆盖同交易多输出、连续更新、容量边界、关闭后拒绝、非法状态仍报错、失败调用不污染下一调用、直接 Backend 入口，以及状态根/快照字节一致。若它仍不足以达标，再决定是否承担跨交易工作状态的复杂性。

不能直接在 RuntimeState 中返回长期共享指针：当前读取会返回独立对象；InvokeItem JSON 往返会把零金额转为省略/nil，把金额文本按 MaxPriceDivisibility 或 GasFeePrecision 重新解析；候选块 Clone、失败隔离和这些规范化行为都需要保留。也不能仅用一个只解码 closed 的投影替换完整读取，因为现路径同时拒绝非法 Decimal 和不一致 InvokeItem，投影会丢失这些校验。

另有一个可独立处理的测试环境问题：启动代码先等 3 秒，再等 3 秒 ticker 首次检查后启动 miner（`server.go:3016` 起），因此 RPC/P2P 可用不代表 POS 已就绪。现有 `getgenerate` RPC 直接返回 IsMining（`rpcserver.go:3064`），后续夹具可复用它等待双节点就绪再注资，不需新接口。该门槛只能避免本次两条启动超时，不会解决七条负载超时，也不应改变本次完整计数。

后续相关 E2E 复验如需进一步定位尾部，建议将 raw DB、链状态事务、资产索引、ffldb Sync/LevelDB 事务及 Pebble Commit 的时长先保存在内存，每块结束只输出一条汇总，并记录 GC/调度时间关联。由 GPT 6 Luna Max 执行同一个 1001 人用例；保持 2/4 秒 ACK 及同步持久化语义。本次没有再次启动节点或运行测试。

本次产物：`contract-only.pb.gz`、`state-callers.txt`、`load-detail.txt`、`save-detail.txt`、`gc-and-logging.txt`、`analysis-summary.json`。前两层 profile 过滤限定的总量已重新核对，未将提案 CPU 混入上述 16.64 秒分母。


### Lifecycle 与默认调用共享一次解码（2026-10-10）

用户已批准并落实上一节推荐的调用范围优化。`Backend.ExecuteTx` / `ExecuteParsedTx` 建立一个临时状态槽，`Lifecycle` 对当前输出完整读取和验证状态，紧随其后的同一 runtime 默认调用一次性消费；后续输出仍重新读取，成功仍保存原 JSON，函数退出使用 defer 清理。直接调用 Backend.DefaultInvoke、没有匹配 runtime 时使用原读取路径。没有新增持久字段、跨交易缓存或通用框架接口，也未改变提案与正式校验的独立执行、准入规则、状态根、数据库同步语义和 ACK 上限。

生产改动位于 `contract/template/backend.go` 与 `managed_balance.go`。新增 `lifecycle_state_reuse_regression_test.go` 对原始/已解析两入口与未启用临时槽的框架路径逐步对照：同交易多输出、连续 delegate 更新、非法资产拒绝、错误退出、直接调用、完整快照字节、状态根、执行记录及 Finalize 结果。复用既有容量边界、关闭/closing 拒绝、失败块隔离、状态解码和其他模板默认调用回归。

GPT 6 Luna Max 执行 18 个指定顶层回归，全部 PASS，退出码 0，包耗时 6.359 秒，完整 JSONL 无 fail，stderr 0 字节。没有运行完整模板包或全仓测试。网络复验仍为原有 `TestNetworkTemplateAutopayCloseAcrossOutputLimit` 单一用例，使用与第一批测量相同的六处临时阶段诊断及节点 CPU profile。


#### 1001 人复验和第一批之后的直接对比

同一 GPT 6 Luna Max 通过正常构建入口仅运行原有网络用例，PASS 378.24 秒，包耗时 383.247 秒，1001/1001 注资全部确认，分批退款、每人完整本金且无重复、Result 输出边界、关闭状态和双节点同步原断言均通过。退出码 0，完整 JSONL 无 fail，stderr 0 字节；两节点和测试进程已退出。

对照上一轮第一批三项优化后的 450.60 秒结果，计时边界、profile 和诊断相同；上一轮 58 个正式区块，本轮 57 个。下表为单次前后观察，区块分组、系统负载和采样可能影响结果，不能将分位数视为稳定承诺。

| 指标 | 第一批之后 | 本次调用范围复用 | 改善 |
| --- | --- | --- | --- |
| core 正式校验 P50 / P95 / 最大 | 451 / 1256 / 1846 ms | 383 / 704 / 1168 ms | 15.2% / 43.9% / 36.7% |
| core 入链 P50 / P95 / 最大 | 546 / 1368 / 1943 ms | 439 / 803 / 1196 ms | 19.6% / 41.3% / 38.4% |
| bootstrap 正式校验 P50 / P95 / 最大 | 438 / 1320 / 1366 ms | 303 / 762 / 1095 ms | 30.8% / 42.3% / 19.8% |
| core 正式校验超过 1 秒 | 4/58 | 1/57 | — |
| core 入链超过 1 秒 | 11/58 | 2/57 | — |
| bootstrap 正式校验超过 1 秒 | 6/58 | 1/57 | — |
| bootstrap ACK 超时 | 9 次，启动 2 + 负载 7 | 2 次，启动 2 + 负载 0 | 总量减少 77.8% |
| core ACK 超时 | 0 | 0 | — |
| E2E 用例耗时，包含构建及等待 | 450.60 秒 | 378.24 秒 | 16.1% |

本轮两条 ACK 超时对应提案 hash `6ccbbff0e6d5…`、`909934770758…`，分别于 23:23:26/29 被 core 收到，core miner 在 23:23:30 才启动；timeout 日志在 23:23:29/31。这两个提案均没有进入正式入链样本。所有超时保留计数，没有修改原 2/4 秒 ACK 上限或矿工就绪夹具。负载阶段观察到零超时，尚不能承诺所有运行均为零。

#### CPU 证据与剩余热点

使用相同两层 profile 过滤（netsync blockHandler，再限定 validateContractBlockFor），本轮正式合约校验累计 11.90 CPU 秒，上一轮 16.64，减少 28.5%。loadRuntimeState 为 7.53 秒（63.3%，上一轮 12.36），saveRuntimeState 2.62 秒（22.0%，上一轮 2.57）。保留的 Lifecycle 完整读取为 4.86 秒，其他读取 2.67 秒；源代码和 profile 调用来源均确认默认业务不再执行紧随 Lifecycle 的第二次读取。保存仍逐次编码原 JSON，不能声称所有整表编解码已消除。

Decimal.UnmarshalJSON 为 3.44 CPU 秒，包含在 load 内，上一轮为 5.66。Autopay.totalDelegateBalance 为 0.55 秒，占正式合约校验 4.62%，上一轮为 0.52 秒；其绝对成本没有明显变化，占比提高主要因为解码减少。它仍不是首要热点，不建议为这项求和新增持久汇总字段。

按正式区块 hash 对齐互不重叠墙钟，57 块累计入链 24.404 秒，其中逐笔 ExecuteTx 13.336 秒（54.6%）、Finalize 4.122 秒（16.9%）、模块准备 1.031 秒（4.2%）、执行后状态根/记录 1.328 秒（5.4%）、协调器其他 0.342 秒（1.4%）、其他正式校验 0.747 秒（3.1%）、校验外入链 3.498 秒（14.3%）。额外提案验证累计 18.709 秒，不包含在入链总量中。

| 仍超过 1 秒的 core 入链样本 | 本轮阶段 | 判断 |
| --- | --- | --- |
| 高度 48，`925e6aa01788…` | 入链 1196ms，正式校验 1168ms，逐笔 ExecuteTx 959ms，Finalize 116ms，校验外 28ms | 尾部主段仍在模板执行；profile 无逐块时间标签，不能把额外等待全部归因于 GC |
| 高度 39，`9c0d36e0b243…` | 入链 1094ms，正式校验 548ms，校验外 545ms | 日志再次与 performUpdateDBInBuffer / updateBasicDB 19 同期；缺少 DB 内部分段计时，不能把全部 545ms 标为 Pebble Commit |

已经明显改善，仍未满足“所有区块入链都小于 1 秒”和“全程 ACK 零超时”。下一步推荐先用已有 getgenerate 等待 E2E 双节点 miner 就绪，以处理独立的启动超时；生产性能继续优先细分索引提交尾部和仍保留的状态读写，收集证据后再审核具体修复。当前不扩大跨交易缓存，不改变同步持久化或 ACK 上限。

证据保存在 `/private/tmp/satoshinet-template-e2e-20261009/call-state-optimization/`：`regressions-after.jsonl/.stderr`、`network-after.jsonl/.stderr`、`call-state-*.log/.pprof`、`comparison.json`、`call-state-stage-summary.json`、`formal-aligned-summary.json`、`formal-aligned-blocks.json`、`contract-only.pb.gz`、`formal-contract-cpu.txt`、`state-callers.txt`、`ack-timeout-aligned.json`、`analysis-summary.json` 和仅包含本批修改的 `production.patch`。六份临时诊断源码归档并逐字节恢复，恢复后本批生产 patch 完全一致。两仓 git diff --check 通过，staged 为空，未 add/commit/push。


### 索引提交附近校验外耗时的分段定位（2026-10-10，待审核方案）

用户要求继续定位上一轮高度 39 的 545ms，并先提供最小方案再修改。本轮仅临时加入墙钟诊断，未实施生产优化。旧轮缺少内层时长，无法事后把 545ms 精确拆开；下面是重新执行同一 1001 人用例、同高度提交旧快照的实测，不能把新数据直接当作旧 hash 的分段时长。

GPT 6 Luna Max 正常构建并仅执行 `TestNetworkTemplateAutopayCloseAcrossOutputLimit`，PASS 511.12 秒，包耗时 514.318 秒；1001/1001 注资，JSONL 无 fail，stderr 0 字节，两节点退出。本轮有额外诊断，同机还观察到独立 PWA 节点构建，因此不用于声称性能回退或收益；bootstrap 保留 16 条 PEER ACK 超时，core 0 条。九份诊断源码在完成后逐字节恢复，包含跨仓库 indexer/db/pebble.go。

#### 高度 39：主段在 Base 同步写批次

core 本轮高度 39 hash 为 `7fb0add56123…`，正式校验 948.591ms，入链 1348.682ms，校验外 400.091ms：

| 互不重叠的阶段 | core 墙钟 | 说明 |
| --- | --- | --- |
| Base 批次构造、地址预取等 | 74.8ms | UpdateDB 总 337.8ms，减去 Flush 边界 263.1ms；UTXO 编码/写批次约 49.9ms，ticker/channel 约 16.8ms，地址写记录约 4.1ms，预取约 2.5ms |
| Base 批次 Commit(pebble.Sync) | 261.9ms | 数据 815229 字节；不能进一步将全部标为文件 fsync，因为此边界还包括 Pebble 内部排队、memtable 和 WAL 等待 |
| 合约索引批次 Commit(pebble.Sync) | 20.0ms | 数据 9234 字节，同一个 baseDB；加上合约快照和编码总 22.7ms |
| 资产索引其他工作及计时日志余量 | 约 23.5ms | 资产索引合计 380.266ms，含 Subtract、普通编译、服务快照及新备份等；各父子层不重复相加 |
| 链状态事务 | 16.5ms | ffldb 元数据/合约状态等事务；此高度未触发 ffldb flush |
| 通知及校验外其他余量 | 约 3.3ms | 通知连接仅 0.173ms，普通 UTXO flush 0.027ms，其余含外层校验计时日志和入链边界 |

bootstrap 同批 Base 为 815229 字节，Commit(pebble.Sync) 249.824ms；合约批次为 9234 字节、19.464ms；Base UpdateDB 总 324.780ms，整个索引缓冲提交 346.713ms。两节点均指向 Base 同步提交，不能仅归因于地址初始化、通知或索引锁。Base CommitBackup 锁等待 core 1.377µs，Subtract 0.887ms；地址预取 core 2.511ms / bootstrap 0.357ms。

调用顺序（恢复后源码行号）：

```text
chain.go:753 assetIndexerMgr.ConnectBlock
  indexermgr.go updateDB -> performUpdateDBInBuffer:513
    contractIndexer.Subtract(contractBackupDB)
    BaseIndexer.CommitBackup:344
      mutex.Lock -> subtractLocked -> backup.UpdateDB:471
        数据预取/批次构造 -> wb.Flush
          indexer/indexer/db/pebble.go:777 Commit(pebble.Sync)
    contractBackupDB.UpdateDB:89
      summary/history JSON -> wb.Flush -> 第二次 Commit(pebble.Sync)
  prepareDBBuffer（新备份）
```

Base 此快照包含 20 块、1096 条待存 UTXO、643 条删除记录、1007 个地址。815KB 不是单独的合约 delegate 表，包含区块、UTXO及ID绑定、地址及新地址ID绑定、STP/通道/核心节点、ticker/holder和同步统计记录。合约 summary/history 在另一个 9KB 批次。没有达到 64MB 单记录或 1280MB 批次阈值，未发现 ensureCapacity 中途额外提交。

全节点 CPU profile 中 performUpdateDBInBuffer / Base.UpdateDB 累计约 0.05 CPU 秒，Pebble Flush 约 0.01 CPU 秒，与上述较长墙钟存在明显差异，支持进一步检查等待；采样并不能独立量化 WAL fsync、调度、磁盘或 GC 等待比例。

#### 另一个独立尾部：300 秒 ffldb 刷新

core 高度 54 的校验外余量 354.320ms，raw block 的 db.Update 占 322.962ms，资产索引仅 9.112ms。本轮实测 cache.flush 总 167.875ms：块文件 Sync 18.645ms，旧缓存 LevelDB 元数据事务约 148.760ms（OpenTransaction 49.881ms、写记录 2.269ms、Commit 96.566ms）；随后当前事务又提交一次 LevelDB，134.001ms（Commit 133.938ms）。两段 301.876ms 包含在 raw DB 边界内，不能与其 322.962ms 再次相加。bootstrap 同类 flush 136.646ms，随后当前事务 59.322ms。

该路径为 ffldb/dbcache.go:567 commitTx 的 needsFlush 分支：先 c.flush 提交旧缓存，再 commitTreaps 提交当前事务。与高度 39 的 Pebble 索引提交是两个不同模块、不同触发点。现有显式 database.Sync、300秒/容量阈值、块文件先于元数据的持久化顺序均保持原样。

#### 最小下一步与优化候选，尚未实施

**推荐先复用 Pebble 已有 Batch.CommitStats()，补齐 Base Commit 的等待归属。** 在一次 Flush 的现有诊断记录中读取 TotalDuration、CommitWaitDuration、SemaphoreWaitDuration、WALRotationDuration、MemTableWriteStallDuration、L0ReadAmpWriteStallDuration。版本 v1.1.5 已提供这些字段，CommitWaitDuration 包含发布 seqnum 和所需 WAL sync 等待；不需要新数据库接口、常驻状态、异步任务或改变 Sync。由 Luna Max 仅重跑原 1001 人用例后恢复诊断。该步骤是定位方案，不把新增日志声称为性能优化。

可供审核的最小生产优化候选是：同一旧快照的 Base 和合约索引写入同一个现有 WriteBatch，再一次 Commit(pebble.Sync)。可在现有两个 UpdateDB 中分离“写入批次”与“Flush/清缓冲”，由 performUpdateDBInBuffer 统一提交；单模块/历史入口仍保留现有 UpdateDB 包装。沿用原 JSON/Gob/protobuf 编码、20/40块窗口、Base锁、失败panic和完整重放原则，不增加持久状态或通用事务框架。

但此候选的可直接消除部分只有本轮第二次同步提交约 19–20ms，主要 250–262ms Base 提交仍会存在，不应承诺消除整个 545ms。它还会将现有“Base可先持久化成功、Contract随后失败”改为同一批次原子提交，因此需要用户明确审核；`TestIndexerCommitFailurePanicsAndRebuildsFromFreshDB` 的旧边界断言也必须按已批准的新原子语义调整，不能为了测试通过直接放宽。收益相对有限，当前不推荐优先实施该结构改动。

ffldb 将旧缓存与当前事务合并成一次元数据事务是另一候选，本轮可避免一段约 134ms 的重复事务，但涉及数据库异常时旧缓存的持久化边界，须另行设计/审核。本轮不混入索引优化，也不直接取消 flush、Sync 或放宽 ACK。

证据：`/private/tmp/satoshinet-template-e2e-20261009/index-commit-investigation/` 下 `network.jsonl/.stderr`、`index-*.log/.pprof`、`stage-summary.json`、`phase-rows.json`、`full-cpu.txt`、`manifest.json`、九份 `.before` 与 `instrumented/`。全部生产诊断文件恢复后 SHA-256 与基线一致；未 add/commit/push。


### Decimal 固定 JSON 编码优化与 ffldb 影响范围（2026-10-11）

用户本轮仅批准 Decimal 编码优化；ffldb 批量写方案仍处于影响评估，未修改。ticker/holder 缓存清理也未实施。

#### ffldb 影响范围

`database/ffldb/dbcache.go:452 commitTreaps` 是整个链库的元数据提交入口。受影响的数据包括块文件位置/块索引、最佳链状态、UTXO、spend journal、锚定记录、Template/EVM/Agent 合约状态，以及启用的交易/地址/过滤器索引。`commitTx` 周期/容量刷新、`database.Sync`、正常关闭都会经过该入口；POS v2 的入链/接受路径显式调用 Sync，因此不限于 300 秒刷新。

底层 `OpenTransaction -> Put/Delete -> Commit` 可以研究替换为 `leveldb.Batch -> Write(Sync:true)`，但改动行数少不代表影响范围小。它改变同步元数据的物理落盘路径（通常由直接生成 SST/提交 MANIFEST 转为同步 WAL，再由存储引擎刷表），必须保留块文件先同步、元数据原子可见性、快照、失败保留缓存和重启恢复保证。当前默认 WriteBuffer 为 4MiB，LevelDB 的更大批次自动使用事务路径，故小批次收益不能推广到大状态；还需核对批次复制带来的峰值内存。

此方案不直接修改 Pebble 资产索引、钱包本地数据库或 STP 自己的数据库；但上述业务依赖的链上交易/合约持久化仍由链库承担。建议独立评审，并以小/大批次、混合 Put/Delete、同步故障、进程中断后恢复、reorg 及 POS 持久化门禁为验证范围，不能只凭 1001 人模板用例通过批准上线。本轮保留 ffldb 原实现。

#### 已批准并实施的 Decimal 优化

仅修改 `indexer/common/decimal.go` 的 `Decimal.MarshalJSON`：保留 Validate 和 Value.String，用 append/strconv.AppendInt 输出原样的 `{"Precision":8,"Value":"123456789"}`。不改变 JSON 字节、字段顺序、数值/精度、nil 与错误行为、解码规则或 Gob 格式。复用原 `common/decimal_json_regression_test.go`，补全部 0..63 精度、零/负数/超过 uint64 的整数与 512 位整数的编码一致性；512 位项只比较编码，不扩大已有解码长度上限。

GPT 6 Luna Max 已执行两个 Decimal 定向回归（count=3）和 10 个模板状态/编码回归，均 PASS。单进程 benchmark 比较直接输出与上一版 struct 编码，三轮中位数如下；这是局部编码函数耗时，不能当作整个入链或 E2E 的提速比例。

| 精度 | 原 struct 编码 | 直接编码 | 耗时下降 | 每次分配 |
| --- | --- | --- | --- | --- |
| 0 | 1004.0ns | 307.8ns | 69.3% | 104B/4次 → 64B/2次 |
| 8 | 1047.0ns | 350.8ns | 66.5% | 104B/4次 → 64B/2次 |
| 63 | 902.1ns | 352.9ns | 60.9% | 104B/4次 → 64B/2次 |

原有 `TestNetworkTemplateAutopayCloseAcrossOutputLimit` 单次 E2E PASS，叶子 366.94 秒、包 369.941 秒；1001/1001 注资，完整 JSONL 无 fail，stderr 0B，测试和两节点均已退出。全部关闭/退款/输出上限/同步断言保持原样。通过临时 Go overlay 仅追加节点现有 --cpuprofile 参数，工作区 helper 无变更，没有安装生产计时埋点。

core profile 继续按 `netsync.blockHandler -> validateContractBlockFor` 过滤，与上一轮 call-state-optimization 同口径：

| CPU 累计采样 | 优化前 | 本轮 |
| --- | --- | --- |
| 正式合约校验 | 11.90s | 10.65s |
| Decimal.MarshalJSON | 1.16s | 0.39s |
| loadRuntimeState | 7.53s | 7.50s |
| saveRuntimeState | 2.62s | 1.89s |

这些是父子调用累计 CPU，不能相加；Decimal 编码 CPU 本次样本下降约 66.4%，正式合约校验约 10.5%。它们来自两次运行，不能等同于逐块入链延迟改善；E2E 用时含构建、出块间隔等，不能据 378.24→366.94 秒单次对比承诺稳定收益。本轮未保留节点 stdout：rpctest TearDown 已删除临时目录，因此 ACK 超时数量没有可用证据，不声称其改善，不为补日志重复执行。

证据目录：`/private/tmp/satoshinet-template-e2e-20261009/decimal-direct-json/`，含本轮修改前文件、隔离 patch/SHA-256、原始 JSONL、benchmark-summary.json、verification-summary.json、两节点 CPU profile、formal-contract-cpu.txt、decimal-encode-callers.txt 与测试 profile overlay。测试后已核对两个修改文件 SHA-256 与测试前一致。未 add/commit/push。
