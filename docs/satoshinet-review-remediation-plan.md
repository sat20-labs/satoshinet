# SatoshiNet 剩余优化与验收计划

更新时间：2026-10-08。本文件只保留尚未实施、待决策或尚未完成验收的事项；已完成优化及历史修复记录已删除。通用 DKVS 设计见 [dkvs-design.md](dkvs-design.md)。

## 1. EVM source 剩余平台决策与验收（原第 11 项）

接口与已确认的验证边界见 [evm-source-blob.md](evm-source-blob.md)。剩余工作：

- 生产编译器需要强制 CPU、内存、输出和时长限制。当前 macOS 主机连沙箱外也不能设置所需内存 limit，HTTP/P2P 接收新源码均失败且不写入。待决定初版只在 Linux 开放源码接收，还是增加 macOS 内存保护机制；未放宽限制或引入容器/监控。证据 `/private/tmp/evm-source-native-macos.log`。
- 在受支持平台，用固定官方 solc 0.8.30 运行完整“编译 → 成功 canonical Deploy 校验 → Blob 首次写入”的正例与反例。固定编译产物的匹配测试、HTTP/SDK/DKVS 测试不能替代这一实机验收。
- 实际节点的本地 RPC、源码 HTTP 和 P2P 接收/同步联合验收仍待执行；当前未部署或操作远程节点。

## 2. 暂缓或不实施

| 事项 | 本轮边界 |
| --- | --- |
| KnownValid 合约状态恢复（原第 2 项） | 侧链/reorg 重连的缺失 post-state 恢复暂缓，不增加在线恢复机制。 |
| BindingSat（原第 5 项） | 保留现有普通花费的输入/输出一致性、承载聪和 Anchor signed invoice 校验；逐资产外查 ticker 注册参数暂缓，不引入临时外部数据作为共识来源。 |
| 资产资源上限（原第 7 项） | 不新增资产项数、字段长度或 Decimal 编码政策上限，沿用现有交易/区块边界。 |
| 通知完整串行化 | 不新增协调锁；钱包/fee estimator 通知严格顺序没有新增保证。证据 `/private/tmp/review13-pending-red-final.log`。 |
| 激活前分叉的获批后缀优先级 | 保持现有选链规则，须上线前统一 canonical 基线；反向收块顺序问题不计为已修复。 |
| 多 Bootstrap 实例 | 仅保留讨论，不实施成员确认/持久协调机制。 |

## 3. 仍需验收的上线事项

- 主网/测试网 POS v2 激活高度 H（同时切换 Anchor 输出授权签名和规范交易编码）、节点/SDK/STP 统一版本与共同 tip/hash，以及每块 combined root 的父状态读取、执行和同步 I/O 开销尚未确定或完成部署验收；不默认回滚或清库。
- 激活切换采用暂停入金的上线安排：H 前暂停新增 opening、splicing-in 和 deposit，待全部在途 Anchor 完成 L1/L2 确认、旧格式 Anchor 不再占用 mempool funding 后，再切换 H 和统一节点/SDK/STP 版本。暂不实现跨 H 的 reservation 转换、重签及旧 mempool 占位清理；已有私人通道预签交易引用 Anchor txid，不能仅重签 Anchor 后继续原流程。
- 补贴已按用户要求设为 0，普通 coinbase 只可领取 BTC fees。此前 104 的历史重放不包含本轮零补贴修改；正式旧链同步/重启须用当前版本重新验证，不把旧结果当作当前结果。旧记录见 [测试网](pos-v2-testnet-replay-104-20261006.md) / [主网](pos-v2-mainnet-replay-104-20261006.md)。
- 网络分区、不同连接方向、精确 ACK 丢失、各接入/刷盘阶段强杀，以及实际插件加载/释放和 DKVS 消息在途退出，仍需完整网络验收。单元测试、编译检查及此前网络结果不代替当前部署验收。

故障约定：意外索引 reorg（包括管理员在线回退）在关库前 fatal 退出，由人工处理，HTTP 的 panic 恢复不能阻止整节点中止；索引写盘故障仍按既定策略中止，修根因后从空库重建，不修补部分提交的数据。正常退出仍等待使用者退出后关库。

本轮修改保留在工作区，不暂存、提交、推送或部署。
