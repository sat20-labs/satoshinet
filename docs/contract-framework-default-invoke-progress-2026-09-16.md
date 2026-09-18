# Framework default invoke：实现与验证记录

日期：2026-09-16
范围：本轮补齐 default invoke 的框架识别、分流、准入、结果绑定及共用测试。本文不是整个合约重构的完成声明。

## 结论

原先 framework 已有 executeDefaultTx 入口，并非完全由各 runtime 自行识别。本轮修复了入口周边的公共缺口，且 contract/framework 全包测试、生产 go build ./... 已通过。完整 go test ./contract/... 仍失败，不能作为可部署版本交付。

## 当前处理规则

1. 没有合约协议 payload 的普通交易，其输出到已注册 runtime 合约地址的每个 UTXO，按 vout 顺序产生一个 default invoke。普通备注 OP_RETURN 不阻止默认调用。
2. DEPLOY、显式 INVOKE、RESULT 和状态根 payload 不产生额外 default invoke。损坏但可识别为合约报文的 header 必须报错，不降级为无参数调用。
3. 同一交易只向每个匹配模块分发一次；模块内的 framework executor 再逐个展开属于该模块的输出。分流不再枚举 Template/EVM/Agent。
4. 框架在进入第一个业务回调前检查本模块全部默认调用的 funding 数量、actor 和固定 gas budget。每个调用 payload 为空，action 为 default。
5. 框架查询 lifecycle。不存在、已关闭或 backend 未处理的 default invoke 进入现有失败退款接口。退款接口不得返回成功状态。
6. 每个返回 outcome 必须绑定当前 txid、唯一 funding outpoint、目标合约和非空唯一 CallID；必须需要 Result，不得伪装成关闭调用，GasUsed 不得越界。成功调用才记入 managed 数量。
7. Template 继续执行其内置默认动作；EVM 使用空 calldata；Agent prediction 没有默认下注动作，走失败退款，不隐式猜测 outcome。
8. 不遍历历史 UTXO 来重放默认调用；RESULT 给合约地址的转账不递归执行 default invoke。

## 本轮公共代码改动

- contract/tx.go：严格识别损坏的合约 envelope。
- contract/default_invoke.go：复用严格 payload 分类；支持 contractType=0 的全类型输出收集；保留 vout 顺序；hash 前检查 nil input。
- contract/framework/default_invoke.go：独立承载默认调用入口与 outcome 约束。
- contract/framework/executor.go：调用前检查 envelope；统一拒绝空 actor/空退款接收人。
- contract/framework/block_split.go：按传入 module registration 分流，去掉默认调用的三类型列表；检查重复注册和 nil 输入。
- contract/framework/block_order.go：与公共 envelope 分类一致；区分真正的 foreign-module invoke 与分类失败。
- contract/framework/result_block_builder.go：先分类再执行；分类错误不可静默跳过。修复 Template/Agent 构造路径对损坏报文返回成功的问题。

旧 coordinator 测试迁移为检查构造后的 Result verification、禁止外部 RESULT 作为 work，仍保留模块隔离和状态根断言。node 状态根测试迁移到公开 ValidateContractBlock 入口，不恢复已删除的旧私有实现。

## 测试

核心共用矩阵直接调用真实 Template/EVM/Agent block builder 与独立 replay，并比较状态根和资金输出。

- 18 个行为子用例：活跃、同一合约多输出、普通备注、未知合约退款、关闭合约退款、业务失败退款。
- 9 个准入子用例：缺失 actor、后续输出为负、损坏合约 envelope；父状态不变。
- 同一交易给三类 runtime 各两个输出：六个 funding outpoint 恰好各结算一次。
- 未知/已关闭合约携带两种业务资产、gas 和聪：核对退款接收人和数额，禁止转给 bootstrap 或记入 managed。
- 注册第四种模拟 module 的默认调用分流。
- RESULT 输出不递归触发默认调用。
- 错误 txid/target/vout、重复 funding、缺少 Result/CallID、伪造 close、gas 越界等 outcome 检查。
- 三种内置类型及第四种模拟类型：退款分支伪造成功不能越过 lifecycle。
- 公共 Result builder 在分类失败、损坏 envelope、nil transaction/input、外部 RESULT 时，不能先执行前面的工作。

新增测试文件：

- contract/default_invoke_test.go
- contract/framework/default_dispatch_test.go
- contract/framework/default_invoke_conformance_test.go
- contract/framework/default_invoke_fixtures_test.go
- contract/framework/default_invoke_assets_test.go
- contract/framework/default_outcome_test.go
- contract/framework/result_block_admission_test.go

第四种 module 测试验证公共分流/约束，不等于已经实现第四种真实 runtime。真实 runtime 的业务 conformance 仍需注册相应 fixture；本轮没有把测试 fixture 注册与生产注册表自动绑定。

## 最新验证结果

执行：satoshinet-test / contract，即 go test ./contract/... -count=1。

最后一次 run_id：20260916T141530-69222-contract。

日志：/Users/yingfeng/mcp/local_access/logs/test-runs/satoshinet-test/20260916T141530-69222-contract.log

通过：contract、contract/framework（包括以上全部新增测试）、contract/engine、contract/oracle。

未通过：contract/agent、contract/evm、contract/template、contract/node。node 已无之前 verifyCombinedStateRoot 不存在的编译错误，但仍有三个 EVM validator 用例失败。

另执行 satoshinet-build，即 go build ./...：exitCode=0。

## 本轮没有完成、不得误报完成的事项

- default invoke 既有费用差异未改：当前 EVM 使用 PlainTxFee；Template/Agent 在有对应 funding 时预留 Result fee。统一 fee policy、全部失败路径由 framework 直接构造和收款，仍属于总重构的后续工作。
- Template 中业务回调返回 error 的分类/回滚仍需全面梳理，不能把任意内部错误统一降格为可退款业务失败。
- EVM 原有 close 输出计数、managed snapshot fixture、trigger gas 及 funding-order 测试仍失败；不得笼统归因于旧测试。
- Template/Autopay 的部署 Result gas、managed backing、跨区块 UTXO 推进及运营 gas 相关测试仍需处理。
- Agent 原有 TestPredictionAgentE2EConfirmAndSettle 在旧 deploy fixture 上因 Result gas 不足失败。
- NetworkExclusive、close authorization 等旧重复分支以及 CallNonce 的完整清理，未在本轮完成。
- SDK 与网络级 e2e 未在本轮完成验证；没有部署、重启生产服务或提交 Git。

完整交付条件仍是：总重构边界全部落实，完整回归及相关 SDK/network e2e 通过，而不是仅以 framework 包测试通过替代。
