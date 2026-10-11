# 模板合约 JSON / gob 编码对比（2026-10-11）

本文保留当时的 gob 诊断结果及实施前状态。后续批准落地的方案采用确定性紧凑二进制，覆盖模板、Agent 和 EVM，见 [合约状态紧凑编码](contract-state-compact-encoding.md)；本文的 gob 数据不能当作新版实现的性能结果。

## 范围与执行状态

用户批准临时诊断，生产优化方案尚未实施。GPT 6 Luna Max 仅运行 `TestDelegateJSONGobDiagnostic`，count=3、benchtime=150ms，三轮 PASS（14.53/13.97/13.79 秒），包 45.364 秒；144 条指标、18 条正常样本验证、18 条边界记录，stderr 0B。原 1001 人网络 E2E 继续暂停。

通过 Go overlay 替换现有 state_codec_test.go 的临时副本，复用 testAutopayRuntime 和 ApplyDefaultInvoke 夹具；三种 delegate 规模分别有 0/20 个待处理项。其余 delegate 使用固定长度诊断地址和合法 Decimal。正常输入以当前 JSON 往返后的状态为基线；边界单独检查未规范化对象。

环境：Go 1.24.13，darwin/amd64，8 个逻辑 CPU，GOMAXPROCS 使用默认值。启动前没有 active go build/compile/link；同机其他钱包测试仍在，共享负载可能影响时间。全部时间/分配为三轮中位数，不作为独占基准，也不推导网络入链的改善比例。

## A：内部 TemplateRuntimeState

JSON 使用当前 json.Marshal / state.UnmarshalJSON；gob 使用每个 blob 独立的新 Encoder/Decoder，包括类型描述。gob 为原始编解码，尚未复现自定义 JSON 校验和规范化，因此不是可直接投产的等价读取实现。状态根验证在计时循环外；没有计入 GetState/SetState 字节复制、数据库同步和状态根计算。

| delegate / 待处理项 | 编码 JSON→gob（ms） | 解码 JSON→gob（ms） | 编码字节 JSON→gob | 字节减少 |
| --- | --- | --- | --- | --- |
| 20/0 | 0.124→0.114 | 0.297→0.273 | 4429→3910 | 11.7% |
| 20/20 | 0.374→0.161 | 1.549→0.303 | 11449→6850 | 40.2% |
| 500/0 | 2.244→1.032 | 7.331→1.339 | 96590→46633 | 51.7% |
| 500/20 | 2.151→1.606 | 6.829→1.513 | 103670→49633 | 52.1% |
| 1001/0 | 3.757→2.341 | 14.201→2.567 | 192783→91223 | 52.7% |
| 1001/20 | 4.310→2.579 | 16.544→2.955 | 199865→94225 | 52.9% |

### 每次调用分配（20 个待处理项）

| delegate | 编码 B/op JSON→gob | 编码 allocs/op | 解码 B/op JSON→gob | 解码 allocs/op |
| --- | --- | --- | --- | --- |
| 20 | 34491→33704 | 445→171 | 129089→50761 | 2276→1003 |
| 500 | 246291→331697 | 3328→2099 | 671428→270367 | 13326→4843 |
| 1001 | 477459→600809 | 6336→4105 | 1232520→501874 | 24855→8853 |

1001/20：编码耗时减少 40.2%，解码减少 82.1%，blob 减少 52.9%；解码分配字节减少约 59.3%，分配次数减少约 64.4%。但编码分配字节增加约 25.8%，不能声称所有内存指标均改善。20/0 的 gob 优势很小，编码/解码分配字节及解码分配次数反而增加。

1001/20 三轮波动：JSON 解码 13.317–24.491ms，gob 解码 2.596–3.898ms；JSON 编码 4.296–6.588ms，gob 编码 1.996–3.519ms。大表趋势明显，但精确比例仍受共享负载和短采样影响。

## B：只替换完整快照的外层编码

JSON 为现有 RuntimeStore.MarshalBinary / DecodeRuntimeStore；gob 编码具体 runtimeStoreSnapshot，通过 sortedKeys/snapshotRuntime 构造，再用 restoreRuntime 重建。内部 State 字节保持原 JSON。避免直接 gob.Encode(store)，后者会调用现有 MarshalBinary 并继续包装 JSON。

| delegate / 待处理项 | 外层编码 JSON→gob（ms） | 解码+恢复 JSON→gob（ms） | 快照字节 JSON→gob | 字节减少 |
| --- | --- | --- | --- | --- |
| 20/0 | 0.027→0.048 | 0.419→0.414 | 6282→5191 | 17.4% |
| 20/20 | 0.053→0.066 | 2.006→1.955 | 15642→12211 | 21.9% |
| 500/0 | 0.446→0.310 | 8.395→8.042 | 129162→97354 | 24.6% |
| 500/20 | 0.600→0.282 | 10.070→8.203 | 138602→104434 | 24.7% |
| 1001/0 | 1.087→0.615 | 19.406→19.395 | 257418→193547 | 24.8% |
| 1001/20 | 0.935→0.829 | 18.853→15.825 | 266862→200629 | 24.8% |

1001/20 的恢复耗时减少 16.1%，快照字节减少 24.8%；编码分配 543096→619173B、20→48 次，恢复分配 1852489→2071477B、24930→25257 次。1001/0 的恢复中位数几乎相同，20/0 外层编码变慢约 73.9%。单独替换外层不能消除逐笔内层 JSON 热点，不推荐作为首项性能修改。

上述字节数是独立编码 blob 大小，未测 LevelDB 压缩后的物理磁盘占用；B 只改变外层，不代表内外层同时改 gob 的性能。

## 一致性与边界结果

正常六组样本三轮均通过：内部结构相等、重编码 JSON 字节相等、runtime 状态根相等、store 状态根相等、完整快照恢复后原 JSON 字节相等。内层 gob 解码后在独立 runtime clone 中恢复为原 JSON，再调用实际 StateRoot；这证明保留规范 JSON 哈希时一致，未证明原始 gob 字节可直接用于状态根。

边界三轮结果一致：

- InvokeItem：JSON 把 GasFee 精度 3→8、InAmt 精度 8→10，非 nil 零金额→nil；gob 保留原结构。数值、合法非空 Param 和 JSON 规范字节保持一致，但结构并不相等。
- 155 位 InvokeItem 金额：当前 JSON 编码成功，解码因整数超过 128 位拒绝；原始 gob 接受并精确往返。
- Decimal 精度 64：JSON 编码拒绝，原始 gob 接受。说明 gob 路径必须补协议校验。
- ManagedBalance 乱序/零额资产：JSON 恢复为 alpha、zeta；gob 恢复保留 zeta、zero、alpha。两者重编码 JSON 和状态根仍相同，故单靠根一致性不足以证明内部状态语义等价。
- ManagedBalance 空 Assets：经过现有 snapshot clone 后，两种恢复均为 nil，未观察到差异。
- ManagedBalance 精度 64：gob 编码接受，但现有 restoreRuntime 的 Validate 拒绝；JSON 在编码阶段拒绝，失败阶段不同。

## 最小优化方案，待审核

推荐先优化内部 template-runtime-state 编码；外层快照维持当前 JSON。主要收益来自 loadRuntimeState/saveRuntimeState，单独修改外层收益较小。

1. 在模板包内部实现独立 gob 状态编解码，并接入 state.go 的 loadRuntimeState/saveRuntimeState。复用当前类型；InvokeItem 必须显式保持现有金额精度、零值→nil、参数一致性与数值长度语义，所有 Decimal 保留协议范围检查。不能只把 json.Marshal/Unmarshal 两行替换为 gob。
2. ContractRuntime.StateRoot 继续生成当前规范 JSON；RPC StateView、共享 Decimal Gob 格式和链数据库同步语义保持原规则。此改动会覆盖共享 TemplateRuntimeState 使用的模板，需对限价单、AMM、Exchange、Autopay 做定向状态/结算回归。
3. 在生产修改前，先用临时原型测包含上述等价校验/规范化的 gob 路径，确认额外成本后再决定。现有 raw gob 的 82.1% 解码减少不能承诺为投产收益。
4. 旧快照内部仍为 JSON，新读取路径需要明确旧数据重建或转换方式；数据处理须单独审核。当前没有实施兼容、迁移、清库或新的持久状态机制。

此方案降低每次整表编解码成本，仍保留逐笔处理整表的调用次数；本轮没有恢复网络 E2E，也没有证明超过 1 秒区块已解决。

## 证据

目录：`/private/tmp/satoshinet-template-e2e-20261011-json-gob`。包含原始 run.jsonl/run.stderr、overlay 测试源码与映射、comparison-summary.json、comparison.csv、raw-codec-records.json、环境/preflight/执行信息和源码 SHA。生产及原测试文件未由本轮修改。

执行命令：

```sh
go test -json -count=3 -timeout=10m -benchtime=150ms \
  -overlay=/private/tmp/satoshinet-template-e2e-20261011-json-gob/overlay.json \
  ./contract/template -run '^TestDelegateJSONGobDiagnostic$'
```
