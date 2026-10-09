# EVM 合约源码 DKVS 实现说明

更新时间：2026-10-07。本文描述本地实现与接口；受支持平台的完整编译/落库及实际节点网络验收仍待完成，见 [剩余计划](satoshinet-review-remediation-plan.md)。

## 验证语义

节点按固定配置编译 Solidity，将 creation init code 和提交的构造参数拼接，与当前 canonical 链上成功 DeployTx 的 `ContractContent` 逐字节比较。部署地址、gas 资金及成功 Result 从本节点的原始主链数据核对，indexer summary 和客户端的 `verified` 不作为证明。

这证明源码在指定编译配置下生成了已部署的完整 init code。源码文本未必唯一；不重放构造函数，不承诺部署瞬间的 runtime hash，也不处理 reorg 后恢复。永久资格和槽位依赖已确认的禁止在线 reorg 故障边界。

## 记录与签名

路径固定为 `/contract/evm/source/<contract_address>`。地址是当前网络、EVM 类型、规范小写编码的 SatoshiNet 合约地址，不能使用 `0x` EVM 地址或地址 hash 别名。

任何有效签名者都可提交，不要求是 deployer；使用现有显式公钥 ECDSA 记录签名接口。SDK 的 `wallet/dkvs.NewSignedRecord` 已识别该固定路径，使用钱包原有 `SignMessage`。签名绑定完整记录和 Value。

记录要求：

- `Version=1`、`Seq=1`、`TTL=0`、`Flags=0`，显式 `PubKey` 为 33 字节压缩公钥，`Signature` 为现有 DER ECDSA 签名。
- `IssueHeight` 不超过节点当前高度；`FeeProof` 为空。成功部署及其费用是该槽位的一次写入资格，无需 AUTOPAY 或另一份存储收费凭据。
- 首次使用 `expect_absent`；完全相同 record/hash 幂等。其他记录不得覆盖，禁止 tombstone、镜像删除或同步快照通过遗漏抹去该记录。
- Value 为以下结构的原始 UTF-8 JSON，使用现有 Blob 上限（1 MiB）；不是普通钱包 Blob 的 DKB1 内容封装。普通 Blob 的所有权和收费规则保持原规则。

```json
{
  "version": 1,
  "language": "Solidity",
  "contractAddress": "<canonical SatoshiNet EVM contract address>",
  "deployTxid": "<lowercase 64 hex>",
  "contractName": "Example",
  "source": "pragma solidity ^0.8.30; contract Example { ... }",
  "compilerConfig": {
    "solcVersion": "0.8.30",
    "evmVersion": "paris",
    "optimizer": {"enabled": true, "runs": 200},
    "metadata": {"bytecodeHash": "none"},
    "singleFileOnly": true,
    "allowImports": false
  },
  "constructorArgs": "<lowercase hex, no 0x; empty if none>",
  "abi": [],
  "initCodeHash": "<lowercase Keccak256 of complete init code plus args>",
  "verified": false
}
```

`abi` 必须是节点编译产物的 ABI；JSON 对象字段顺序/空白可不同，数组顺序必须相同。`initCodeHash` 只是重复核对字段，不能代替完整字节比较。拒绝未知 JSON 字段及尾随内容。不得提交非空 `runtimeCodeHash` / `verifyError` 或非零 `submittedAt` / `updatedAt`；客户端 `verified` / `verifyStatus` 被忽略，查询由节点固定返回 `verified=true`、`verifyStatus="verified-init-code"`。

## 编译与部署证明

只使用官方原生 solc 0.8.30，按二进制 SHA-256 白名单验证。运维可用 `--evmsourcesolc=<absolute path>` 指定，未指定则从 PATH 查找 `solc`；不自动下载或切换版本，当前主机的 0.8.35 不会被接受。

官方二进制 SHA-256（来自 [solc-bin 发布清单](https://github.com/argotorg/solc-bin/tree/gh-pages)）：

| 平台 | SHA-256 |
| --- | --- |
| Linux amd64 | `f3e987dc6ecebd4bd350c48edcbc320b46cf9e3109bd3fc3d88f1acaf4c428f7` |
| macOS amd64 | `738dcdc6afddeb505ee4e4ef24f1c1fdba2b8c924e614cbbf5801a5b062dd683` |

固定文件名 `Contract.sol`，使用 standard-json 和上述完整编译配置。禁止 imports，关闭文件导入回调，拒绝未链接库占位符。临时工作目录在结束后删除；单进程编译、不排队，已有编译进行中时拒绝新请求。资源限制为墙钟 15 秒、CPU 10 秒、内存地址空间 512 MiB、stdout 8 MiB、stderr 64 KiB；任何限制设置/编译/验证失败均不得写入。

**平台尚有待决策：** Linux 使用系统 virtual memory limit。当前 macOS 不支持所需的内存限制，新源码记录的接收会失败（包括 HTTP 写入和 P2P 同步），只能查询库中已有记录；尚未添加 macOS 监控/容器替代，也未放宽资源约束。二进制进入白名单不代表该平台已通过资源保护验收。真实受限编译失败日志：`/private/tmp/evm-source-native-macos.log`。

使用节点已有本地 RPC，读取已确认交易所在块，核对该高度的 canonical hash 及原始 block hash，再从原始块找到 DeployTx。检查部署 payload、该地址唯一的 funding output、部署所需 gas reserve，以及同块中成功且花费该 funding outpoint 的合法 Result。链上成功部署证明依赖该 canonical 块已经经过节点共识校验；测试夹具中的 raw block 不等同于完整网络部署验收。

本地 RPC 未启用、txindex 不能定位历史交易、原始块不可读取或部署失败时，不能写入源码。验证和编译在 DKVS 提交锁外执行，提交时仍使用原有 CAS 并发检查。

## HTTP 与同步

`POST /v3/contracts/:contract/evm/source` 现在接收签名记录：

```json
{"record": {"Version": 1, "Key": "/contract/evm/source/<address>", "Seq": 1, "TTL": 0, "Flags": 0, "PubKey": "<base64>", "Value": "<base64 JSON>", "FeeProof": null, "Signature": "<base64>"}}
```

`wire.DKVSRecord` 的 JSON 字节字段按 Go JSON 规则使用 base64；生产者应序列化 SDK 签好的完整记录，不手工修改签名字段。HTTP 路径须与 record key 相同。未签名的旧 metadata 请求已移除；验证失败返回错误，槽位不被占用。相同记录重试成功，不同记录冲突。

`GET /v3/contracts/:contract/evm/source` 保留 metadata 查询形状，从系统 Blob 读取并返回节点生成的验证状态；找不到时沿用现有 `code=-1` 响应。`GET /v3/contracts/evm/compiler-config` 返回固定编译配置。

P2P 实时接收、记录集和 path snapshot 安装也执行源码验证；无 verifier 的实例拒绝新源码写入。已有永久槽位不能被另一条记录或空快照替换。查询只检查已接收记录的签名/包络，不反复编译或在读锁内调用链 RPC。

contract indexer 只保存链上部署/调用数据，已移除 `contract:v1:evm:source:*` 的旧读写入口。旧数据库记录没有迁移、回退兼容或自动清理。

## 验证范围

SDK 签名、DKVS 不可变性/删除与快照保护、HTTP 入库和 manager 查询状态有独立回归。官方编译产物固定向量验证 init code/参数/ABI/hash，以及成功 Result 必须花费匹配 funding output；这些测试不替代受限编译器的完整正例。

Linux 受限编译及真实节点本地 RPC/HTTP/P2P 联合验收仍待完成，未进行远程部署。
