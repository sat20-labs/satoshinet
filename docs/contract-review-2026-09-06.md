# SatoshiNet 智能合约审查与修复记录（2026-09-06）

## 范围与结论

本次审查针对当前完整实现，包括已提交代码和工作区改动：EVM、template，以及两者共用的执行、Result 结算、状态存储、区块验证和挖矿接入路径。不是仅检查未提交 diff。Agent 合约及其专用业务实现按要求排除。

原先确认的八项问题已按“先构造失败测试，再修改，再验证”完成修复。补充全代码审查又复现了三项 P1：用户确认后，关闭清理 trigger、多个 trigger 的预算问题已修复；第三项通过禁止直接执行另一业务合约代码，阻断了原先复现的内部 CALL 路径。用户已确认不支持 Result 自动调用后继合约：Result 向 B 转入资金仅作结算，不执行 B，以阻断无限链式反应。现有测试通过不代表已完成完整安全审计或生产数据升级演练。

审查时 HEAD 为 `071197e2b8165594ce1b49a5778528181ed370f4`，验证对象包含未提交改动。未执行暂存、提交、推送或远程部署。原有 AMM 精度改动和 mining 暂存内容保留。

## 原八项修复

| 项目 | 修复与失败测试证据 | 当前回归测试 |
| --- | --- | --- |
| 1. Template 不同合约的 itemID 冲突 | 按合约分别构造结算计划，以合约地址和 itemID 共同关联记录；原实现会串用输入、遗漏另一合约的计划或错误关联资产 intent。 | `TestReviewSettlementItemIDsAreContractLocal`，覆盖资金/费用、缺失计划和资产 intent |
| 2. EVM 重复部署使区块执行直接报错 | 仅成功部署校验返回地址；失败部署使用原先推导的地址记录失败和退款。旧实现将 geth 失败返回的零地址判为地址不匹配。 | `TestReviewDuplicateDeployIsRefundableFailure` |
| 3. Exchange 不可分资产与分数价格精度 | 先按资产 A 的实际精度确定成交量，再计算 minOut 和资产 B 成本；B 成本使用整数精确乘法并按其有效精度向上取整，余款退款。旧实现可能扣除无法实际交付的小数资产；追加测试还复现了整数资产乘分数价格被提前截断的问题。 | `TestReviewExchangeIndivisibleAssetRefund`、`TestReviewExchangePrecisionConservesPartialFill`，包含价格 `0.333` |
| 4. EVM 构造失败遗漏非 gas 资产退款 | 失败部署复用公共 funding 退款逻辑，返还普通 satoshi 与其他非 gas 资产。旧实现未生成这些退款 intent。 | `TestReviewFailedConstructorRefundsNonGasFunding`，覆盖 REVERT 和 OOG |
| 5. 累计状态超过 16 MiB 后无法保存 | EVM/template 大快照分块保存，保留原状态编码和状态根算法；小快照仍写原始格式。读取校验分块顺序、长度和摘要，删除/覆盖同步处理分块。旧实现拒绝正常累计增长的大状态。 | `TestReviewGrowingEVMStatePersists`、`TestReviewTemplateLargeStateAndLegacyEncoding`，覆盖往返、旧格式、覆盖、删除及损坏检测 |
| 6. 父块状态可能误用其他分支 tip | 沿指定父块的真实祖先查找状态，并纳入已验证、尚未持久化的中间状态；未知父块不再回退到无关 tip。 | `TestReviewParentStateNeverUsesUnrelatedTip`、`TestReviewSparseParentStateUsesItsOwnAncestor`、`TestReviewSparseParentStateUsesValidatedAncestor` |
| 7. EVM msg.value / balance 未反映资金 | `msg.value` 读取实际 funding 输出的物理 satoshi；余额读取可支配普通 satoshi 及待结算资产变化，不另建持久化余额账本。旧实现对应读取为零。原生转账范围限制见后文。 | `TestReviewNativeValueAndUTXOBalance`、`TestReviewConstructorReadsFundingValue`、`TestReviewNativeTransferCannotCreateUnsettledBalance` |
| 8. 生产执行环境缺少完整 BLOCKHASH 历史 | 验证和挖矿入口沿当前父块所在分支装载最近 256 个历史块哈希。旧路径缺少较早祖先哈希。 | `TestReviewProductionEVMBlockHashes` |

回归测试位于 `contract/{evm,template,node}/review_regression_test.go`。没有用跳过断言或跳过既有 E2E 的方式使测试通过。

## 新增发现与后续处理

最初三个诊断测试的 PASS 表示成功复现了修复前的缺陷，不能解释为修复通过。它们通过 Go overlay 运行，没有混入正式回归测试。后续正式回归位于 `contract/evm/trigger_review_test.go`。

### 已修复 P1：关闭合约后遗留 trigger，到期时阻断区块执行

位置：`contract/evm/statedb.go` 的 `CloseContract`。

`CloseContract` 只记录 closed 标志，未清理该合约注册的 trigger。未来 trigger 到期后，`ExecuteTrigger` 在预算检查之前返回 `trigger contract does not exist`，错误上抛到区块执行/Result 构造。正常关闭合约也能留下这样的状态。

复现：`TestReviewProbeClosedTriggerHaltsBlock`，注册未来高度任务后关闭合约，到期空块执行报错。

用户确认智能合约尚未正式上线，无需历史状态兼容。本轮在关闭时通过已有 journal/RemoveTrigger 删除所属任务，不扫描或迁移旧数据库。相同 ID 的其他合约任务不受影响；状态回滚会同时恢复 closed 标志与任务。正式回归 `TestReviewCloseRemovesOnlyOwnedTriggersAndReverts` 在修复前失败、修复后通过，并验证到期区块正常执行。

### 已修复 P1：多个 trigger 重复使用同一份 gas 余额

位置：`contract/evm/trigger_budget.go`、`contract/evm/backend.go` 的 `ExecuteTrigger`。

每个 trigger 的预算检查都重新累计原始 UTXO，没有扣除同块前序任务已占用的费用和资金。余额只够一次调用时，两个到期任务仍能分别通过检查并执行，最终合并 Result 因 `insufficient funds` 失败。

复现：`TestReviewProbeTriggersReuseGasBudget`，两个耗尽各自 gas limit 的到期任务，共用仅足够一个任务的 gas 资金；Result 构造失败。

已按确认方案修复：在同一合约的 pending 记录上扣除实际执行费、Result 费用、待退款和资产支出，再预留下一任务的最大费用。费用计算提取为 Result 共用的 `RecordResultGasFee`，保留原公式和逐条向上取整规则。资金或剩余区块 gas 不足时保留任务；资金充足时继续执行。执行中的余额视图同时保护 gas 预留，避免预编译转账把结算费用也转走。

正式回归覆盖一份/两份任务预算、区块 gas 用尽延期、后续补资执行、待退款/转账占用、逐条精度取整、不同合约预算隔离、拒绝转出费用预留与正常转出剩余资产。核心失败记录为 `/tmp/contract-trigger-review-red.log` 和 `/tmp/contract-trigger-reserve-red.log`。

### 已阻断原始 CALL 路径：嵌套 EVM 资产转移成功后，Result 不支持结算

位置：`contract/evm/runtime.go` 的 `commitCapturedEffects`、`contract/framework/canonical_result.go` 的 `validateIntentContracts`。

A 调用 B，B 使用资产预编译接口转移自身资产时，VM 可以成功并产生 `From=B` 的 intent。但顶层执行记录归属 A，Result 框架随后以 `result plan cannot mix contracts` 拒绝构造结算。问题在执行成功之后才暴露，影响整个 Result 构造。

修复前的具体例子：用户调用 A，A 普通 CALL 到 B，B 依自己的合约逻辑把自身 11 sat 中的 7 sat 转给用户。执行层检查 B 余额足够，记录 `From=B, To=用户, Amount=7`；顶层记录却属于 A，最终按 A 构造 Result 时无法承接这条 B 的支出。VM 阶段记录的只是待结算指令，尚未通过 Result 花费链上 UTXO。这不是 A 绕过 B 的合约逻辑直接盗用资产，而是执行层允许的行为超过结算层支持范围。

复现：`TestReviewProbeNestedAssetTransferCannotSettle`，B 有足够余额，嵌套调用成功，但 A 的 Result 无法承接 B 的资产 intent。

最初提出的最小方案是在提交状态前拒绝 Result 无法承接的跨合约资产 intent。用户随后明确了“跨合约只能通过交易触发”的边界，并要求代码禁止 A 直接 EVM CALL B；最终实现以这项明确要求为准，不保留跨业务合约的同步调用能力。

最终修复位于 `contract/evm/call_boundary.go`：执行用 StateDB 在 geth 读取嵌套调用目标代码时校验当前帧与顶层帧的代码地址，禁止 `CALL`、`STATICCALL`、`DELEGATECALL`、`CALLCODE` 执行另一业务合约代码。返回空代码使 B 不执行任何 opcode，并记录不可被 Solidity 捕获或忽略的错误，使本次顶层执行及其资产/trigger 副作用回滚。构造函数调用 B 也受相同限制。顶层交易直接调用 B、同一合约自调用、预编译接口以及 EXTCODECOPY 等代码查询保留。

回归位于 `contract/evm/call_boundary_test.go`：修复前四种调用、构造函数调用和副作用用例均失败，日志 `/tmp/contract-call-boundary-red.log`；修复后验证 B 的 opcode 执行计数为零、A 状态恢复、资产/trigger 无残留，以及拒绝调用后仍生成正常退款 Result。已有嵌套 STATICCALL 测试按新的跨合约边界改为断言明确失败，直接 STATICCALL 预编译的只读测试继续通过。

**后续架构澄清：**用户明确聪网 EVM 的跨合约交互应通过交易触发：`Call TX → A → Result A（输出调用 B）→ B → Result B`。因此，上述拒绝跨合约 intent 只能作为阻止无法结算行为的保护，不能作为该调用链的完整实现。同步进入另一业务合约代码的 EVM CALL 不应被当作这一模型已支持的跨合约入口；预编译接口本身使用 CALL，不能不加区分地禁用整个 opcode。

当前源码尚未实现 Result 驱动的后继调用，静态证据如下：

- `contract/default_invoke.go:37`：默认调用识别排除带合约 payload 的交易，Result 输出到 B 不会自动触发默认调用。
- `contract/evm/backend.go:307`：当前将本轮所有待结算记录生成一个 Result，应用 UTXO overlay 后结束，没有继续调度其输出指向的 B。
- `contract/framework/result_verifier.go:153`：有结算计划时只接受一个 Result，不能直接承接 A、B 各自的多个 Result。
- `contract/framework/block_split.go`：Result 单独归类后跳过，不作为后继调用的输入来源继续识别。

符合用户模型的交易调用链需要同时覆盖：标识调用输出与普通找零/资金留存、按依赖调度 A/B 并各自结算、保证 B 的 caller 为 A、为 B 明确 funding/gas 与调用参数、限制调用链资源，以及让出块和验块重放相同顺序。当前“每模块一个 Result”的规则需要相应调整。是否同块完成、超限时如何处理及失败边界尚未确定；这部分仅核对代码并记录方案，未修改实现。

**Result A → B → Result B 包内验证：**在用户要求构造用例后，使用独立 Go overlay 做了正反对照，未修改生产代码。普通交易向 B 提供 funding 时，B 的默认入口写入状态，并生成消费该 funding 输出的 Result，控制组通过。A 的用例只调用资产预编译，将 1000 sat 和 1000 gas 资产通过 Result A 转到 B，输入参数固化在 A 的测试字节码中，没有内部 EVM CALL B。验证 A 执行成功、转移指令与 B 输出金额正确后，实际观察到：只有一笔 Result，B 执行记录为零，B 的状态标记为零，Result A 被识别出的默认调用输出为零。“应生成两笔 Result，且 Result B 消费 Result A 对应输出”的断言失败。日志 `/tmp/result-cascade-probe.log`，overlay `/tmp/result_cascade_overlay.json`，测试源码 `/tmp/result_cascade_probe_test.go`。

这说明当前实现只完成了向 B 地址转入资金，没有自动执行 B。它不构成“UTXO 无法表达此机制”的证明：后继交易可以引用前序交易输出，但必须是独立交易；不能在当前汇总成同一笔 Result 的规则中让该交易花费自己的输出。是否引入这类自动调度属于协议与架构决策，不能通过放宽内部 EVM CALL 限制替代。

**真实网络用例：**`integration/contract_e2e/result_cascade_network_e2e_test.go` 新增 `TestNetworkEVMResultOutputInvokesNextContract`，采用本地 bootstrap/core 与 fake L1 fixture，先真实部署 B 并验证普通默认交易可使 B 生成自己的 Result，再真实部署 A。A 仅调用资产预编译，固定向 B 转出 1000 sat 和 1000 gas 资产。最终验收要求：同一区块内存在另一笔 Result B，确实花费 Result A 转给 B 的那个输出；不通过手工补发 B 调用冒充自动链路。该功能当前未实现，用例保留明确的失败验收断言，受 `rpctest` build tag 控制。

网络首轮曾在 A 显式调用阶段先遇到 `evm result build: insufficient funds`，尚未到级联断言，日志 `/tmp/sat20-result-cascade-network-first.log`，不能将此轮当作自动调用失败的证据。静态追踪表明：当前 funding gas 未被认领时会退款，同时直接转出该 gas 资产可能重复占用资金；这是需要独立审查的资金预留问题，本轮未修改生产逻辑。最终测试将 A 改为普通默认 funding 交易，并提供 2000 sat，转给 B 1000 sat 后给 A 自身的 Result 留足普通费用，用于隔离验证 Result 驱动调用机制。

诊断源码：`/tmp/contract_review_probes_test.go`；overlay：`/tmp/contract_review_probes_overlay.json`；日志：`/tmp/contract-review-probes.log`。

```sh
GOCACHE=/tmp/sat20-rgb-send-tests-go-cache go test \
  -overlay /tmp/contract_review_probes_overlay.json ./contract/evm \
  -run TestReviewProbe -count=1 -v
```

## 验证结果

### 包测试与 race

以下六个包全部通过；最后补充分数价格修正后重新运行 template 包，通过。

```sh
GOCACHE=/tmp/sat20-rgb-send-tests-go-cache go test \
  ./contract/evm ./contract/template ./contract/node \
  ./contract/framework ./contract/engine ./contract -count=1
```

最终代码的 EVM、template、node 三包 race 测试全部通过：

```sh
GOCACHE=/tmp/sat20-rgb-send-tests-go-cache GOMAXPROCS=4 \
  go test -race -p 2 ./contract/evm ./contract/template ./contract/node -count=1
```

日志：`/tmp/contract-review-packages.log`、`/tmp/contract-review-template-final.log`、`/tmp/contract-review-race-final.log`。macOS 链接器输出 `LC_DYSYMTAB` 警告，但没有测试失败或 race 报告。

### Luna max 本地网络 E2E

按用户要求由 Luna max 子 agent 验证。每组通过 rpctest 启动本地 bootstrap/core 节点，配合 fake L1 indexer；实际部署、广播、出块和结算，不访问远程生产节点。

以下 19 个不同网络用例全部通过，没有 skip：

- Solidity 部署、调用、资产结算：1 项。
- Template / EVM close / 同块混合状态根 / rollback 等：18 项，覆盖 LimitOrder 多笔撮合、部分成交与退款，Exchange funding/buy/close，AMM 顺序定价、初始 K、滑点、买卖及 LP 增减。

```sh
GOCACHE=/tmp/sat20-rgb-send-tests-go-cache go test -tags=rpctest \
  ./integration/contract_e2e \
  -run '^TestNetworkSolidityContractsDeployInvokeAndAssetSettlement$' -count=1 -v

GOCACHE=/tmp/sat20-rgb-send-tests-go-cache go test -tags=rpctest \
  ./integration/contract_e2e \
  -run '^TestNetwork(Template|EVM|AMM|LimitRefund)' -count=1 -v
```

上述第二组使用的二进制构建早于最后一次 Exchange 精度修正。因此，修正后又重新构建节点和 wallet 插件，单独运行并通过 `TestNetworkTemplateExchangeDefaultFundBuyAndClose`。不能将三次运行表述为全部使用同一份最终二进制。

日志：`/tmp/sat20-contract-e2e-solidity-final.log`、`/tmp/sat20-contract-e2e-template-final.log`、`/tmp/sat20-contract-e2e-template-exchange-final.log`。

Agent 网络用例按范围排除。Autopay 有包内测试覆盖，当前 E2E 目录没有独立 Autopay 网络用例，不能宣称已完成该模板的独立网络验收。

### 后续两项 trigger 修复的验证

六个相关包已重新运行通过，日志 `/tmp/contract-trigger-review-packages.log`。新增边界测试后，EVM/framework/template/node 四包 race 全部通过，日志 `/tmp/contract-trigger-review-race.log`。

Luna max 对本轮最终代码重新构建节点和 wallet 插件，19/19 项网络 E2E 全部通过，无 FAIL/SKIP：Solidity 1 项（命令耗时 57.822 秒），Template/mixed 18 项（433.097 秒）。日志分别为 `/tmp/sat20-contract-trigger-e2e-solidity.log`、`/tmp/sat20-contract-trigger-e2e-template.log`。两轮节点构建时间分别为 2026-09-06 16:06:53、16:07:44，均晚于生产代码最后修改时间；构建版本 Go 1.24.13，tags `rpctest,wallet_plugin`，`vcs.modified=true`。

新增 trigger 故障由包内区块执行、Result 构造测试直接覆盖；既有 19 项网络 E2E 用于相邻流程回归，没有将它们表述为新 trigger 场景的独立网络验收。

### 禁止内部跨合约调用的验证

六个相关包测试全部通过（`/tmp/contract-call-boundary-packages.log`）。EVM/framework/template/node 四包 race 全部通过（`/tmp/contract-call-boundary-race.log`）。Luna max 使用本轮新构建的节点与 wallet 插件重跑非 agent 网络 E2E，19/19 项全部通过，无失败、无跳过：Solidity 1 项，命令耗时 31.196 秒，日志 `/tmp/sat20-contract-call-boundary-e2e-solidity.log`；Template/mixed 18 项，命令耗时 435.434 秒，日志 `/tmp/sat20-contract-call-boundary-e2e-template.log`。

### Result 驱动调用链的历史探测与最终边界

Luna max 已运行新增 `TestNetworkEVMResultOutputInvokesNextContract`，耗时 44.029 秒。普通默认交易调用 B 的控制组通过；A 成功执行，Result A 确实向 B 输出 1000 sat 与 1000 gas 资产，但没有产生消费该输出的独立 Result B，测试在第 82 行的同块自动级联断言失败，无跳过。Result A 为 `a5e47d31f8235abc070f0e9de949caf5c1a4f6aa6705fcd9e73c34edf2a5094d`，B funding 为其输出 0；默认调用输出识别数为零。日志 `/tmp/sat20-result-cascade-network.log`。

此前 19 项回归通过不包含本新增用例；新增用例目前明确失败，不能宣称当前完整网络测试集全部通过。本轮仅增加测试与审查记录，未修改生产代码。结论限于现有框架未实现同块 Result 自动级联，不代表 UTXO 原理上无法支持后继交易。

**最终决策（覆盖上述探索方案）：**用户确认不支持 Result 自动触发后继合约，避免无限链式反应。允许 Result A 向 B 转入资产，但 B 不因此执行；内部跨业务合约 EVM CALL 仍被禁止。既有默认调用识别和 Result 分类逻辑已经阻断此路径，无需新增调度。网络用例改名为 `TestNetworkEVMResultOutputDoesNotInvokeNextContract`，改为断言默认调用输出为空且没有自动 Result B，同时保留普通交易调用 B 的成功控制组。以上 RED 结果保留为决策前的历史探测记录，不再作为待实现功能。

最终禁止级联的网络回归已由 Luna max 运行通过：`go test -tags=rpctest ./integration/contract_e2e -run '^TestNetworkEVMResultOutputDoesNotInvokeNextContract$' -count=1 -v`，耗时 64.099 秒，exit 0，无 SKIP。普通默认交易调用 B 的控制组通过；Result A 向 B 转入 1000 sat 和 1000 gas 后，默认调用输出为零且没有自动 Result B，符合最终决策。日志 `/tmp/sat20-result-no-cascade-network.log`。

## 升级与功能边界

- 本次验证了 EVM/template 原始小快照格式可继续读取，以及新大快照的分块往返；没有使用今年 4 月底生产数据库副本进行完整升级演练。核心节点区块、通道合约及通道状态能否整体升级，不能仅由本轮合约测试作结论。
- 未修改通道数据格式。状态分块不改变合约状态编码或状态根，但旧二进制不支持新产生的分块快照；不承诺二进制降级读取。累计状态仍然整体序列化，历史快照的长期存储增长问题没有在本轮重构。
- 原生 EVM 余额支持采用最小范围：补齐 funding 的 `msg.value` 和合约可支配普通 satoshi 余额读取；非零嵌套原生 value 转移不支持，SELFDESTRUCT 明确失败。实际资产转移仍需使用已有资产预编译和 Result TX，没有宣称完整实现 Solidity 原生转账语义。
- 两项 trigger 问题已修复，第三项原始复现中的直接跨合约 CALL 路径已阻断。Result 驱动的后继合约自动调用明确不支持，结算输出不得成为新的默认调用。核心节点整体生产数据升级演练仍未执行；合约自身没有已正式上线的数据需要迁移，是用户本轮明确的前提。
