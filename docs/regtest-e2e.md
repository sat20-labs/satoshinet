# 本地 regtest 验收环境

2026-10-12：用于 SDK/PWA 的独立 E2E 网络。用户最新指定在103 corenode与104 bootstrap建立专属regtest，接本机PWA；该阶段不对既有共享测试网执行3422回滚。

节点使用 `--regtest`。聪网 regtest 有独立 P2P magic 和 genesis，地址使用 bcrt，与 BTC L1 regtest 对相同公钥生成的地址一致。内置索引器的数据库目录和 HTTP proxy 都使用 `regtest`。SDK/STP 插件读取可执行文件旁的 `conf.yaml`，该文件也必须配置 `chain: regtest`；真实 STP 环境使用已有 `dev`。

L1 使用 Bitcoin Core31.0、实际 L1 indexer 和 Electrs。当前本地 RPC 为 `127.0.0.1:18453`，L1 indexer 为 `http://127.0.0.1:30143/btc/regtest`，Electrs HTTP 为 `127.0.0.1:30043`。测试配置只能连接本地端点，不回落公网。旧 fake L1 的通道管理与通道合约测试继续使用原环境。

regtest POS 出块目标为2秒，检查间隔1秒。这些值从矿工实例的链参数选择，其他网络继续使用原默认值/环境变量。prewarning、ACK超时和节点资格校验保留。正常定时出块需要有效 mempool 交易；空 mempool 不定时产空块。

CSV、Segwit、Taproot 沿现有 BIP9 状态机暖机，第3块开始激活。POSV2 默认不另行激活。现有离散 `generate` RPC 也拒绝空交易。用户明确不允许空块，先用真实L1 anchor tx取得聪网资金，再由夹具提交有效交易推进暖机和后续块。原空块候选已撤回；其诊断链不能计为正式验收。暖机后须查询实际 deployment 状态，再启用定时挖矿。

节点重启保留原 block DB、索引器、钱包和 STP 数据。首次建立该独立网络使用专属目录，后续继续使用同一目录；配置支持不会自动迁移旧的不完整 regtest 数据。

DKVS 数据库独立保存在 `data/db/dkvs/<network>`，例如 `data/db/dkvs/regtest`；`data` 对应配置的数据根目录。`data/db/indexer/<network>` 保存可从链重建的索引，重建索引时应保留 DKVS 目录。DKVS 含有不能通过链重建的数据，需单独备份；记录原有的 TTL 和付费保留规则继续生效。本次路径调整不读取或自动迁移旧的 `data/db/indexer/<network>/dkvs`。

现有 SDK runner 的真实节点入口为 `sdk/e2e/real_regtest_environment_test.go`。它接受 `SAT20_REAL_L1_INDEXER`、`SAT20_REAL_TEST_WALLETS` 和 `SAT20_REAL_REGTEST_RUNTIME`，复用现有插件构建与节点 runner，在持久目录启动固定 bootstrap/core 身份。私密钱包 fixture 位置由 `knowledge/pwa-test-wallets.md` 登记，不写入文档、日志或命令输出。

验证顺序：参数/地址与正常网络对照 → 同DB重启 → 真实暖机与激活 → SDK实际资金/资产准备 → 有效交易下的2秒节拍 → SDK/PWA业务验收。参数测试通过不能代替真实节拍和业务验证。当前状态及证据见 `knowledge/sdk-pwa-e2e-real-l1-plan-20261012.md`。
