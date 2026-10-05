# DKVS 外部集成契约

更新时间：2026-10-04  
性质：DKVS 与 DID、费用和系统授权模块之间的长期接口边界。  
核心协议与同步语义以 [dkvs-design.md](./dkvs-design.md) 为准。

本文不维护实现进度或 open decisions。尚未配置的主网参数集中记录在“部署参数”一节。

---

## 1. 总体边界

DKVS core 负责：

- key/namespace 解析；
- record 签名与 Seq/CAS；
- Wallet request authorization；
- TTL/expiry；
- fee proof 调用；
- 当前状态存储；
- Wallet current-set sync；
- P2P current-state propagation。

DKVS core 不负责臆造：

- Ordinals DID owner；
- service DID 归属；
- AUTOPAY 主网经济参数；
- system signer 治理；
- checkpoint 链上共识。

外部模块不可通过“resolver 不可用时默认放行”的方式降级。

---

## 2. DID Resolver

接口：

~~~go
type DIDResolver interface {
    ResolveName(name string) (DIDIdentity, error)
    ResolveService(serviceName string) (DIDIdentity, error)
}

type DIDIdentity struct {
    CanonicalName  string
    NameID         string
    SigningKeys    [][]byte
    OwnerAddresses []string
    AddressParams  *chaincfg.Params
    Active         bool
}
~~~

### 2.1 Required semantics

ResolveName 用于：

~~~
/name/<did>
~~~

ResolveService 用于：

~~~
/svc/<did>/...
~~~

resolver 必须返回**验证时的当前 owner**。

DKVS 接受写入的条件：

~~~
identity.Active
AND (
    record pubkey ∈ SigningKeys
    OR p2tr(record pubkey) ∈ OwnerAddresses
)
~~~

### 2.2 所有业务写入都解析当前 owner

/name 和 /svc 的每次业务写入都必须调用 resolver。

不再采用：

~~~
已有 key + 相同 pubkey => 跳过 resolver
~~~

因此 DID 转移后：

- 旧 owner 即使持有历史 record 私钥，也不能继续覆盖；
- 当前 owner 可以接管已有 key；
- stored record 的读取不需要每次重新 resolve，但新的 mutation 必须 resolve。

### 2.3 DID transfer

接收钱包使用 Wallet SDK 的 ResignDIDRecords：

~~~
读取完整 /svc/<did> 当前集合
+ 可选 /name/<did>
→ 验证所有旧 records
→ 保留 value / fee mode / absolute expiry
→ current owner 全部重签
→ one batch-CAS atomic commit
~~~

每个接管 record 使用：

~~~
Seq = old.Seq + 1
IssueHeight = current height
~~~

有 TTL 的 record 不延长原绝对 expiry。

如果完整 DID collection 超过单 batch 上限，则整体拒绝；不允许部分重签。

### 2.4 默认安全行为

默认 resolver 返回 DID resolver unavailable，因此 /name、/svc 在没有真实 resolver 时保持关闭。

resolver 内部错误必须向上返回，不得 fail-open。

---

## 3. L1 / HTTP Resolver adapters

### L1NSResolver

当前 L1 适配器可以读取：

~~~
GET <base>/ns/name/<name>
~~~

典型响应：

~~~json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "name": "alice",
    "address": "bc1p...",
    "utxo": "txid:vout"
  }
}
~~~

data.address 作为当前 owner address。DKVS 从 record pubkey 派生 P2TR address 并比较。

### HTTPDIDResolver

可选通用 HTTP adapter：

~~~
GET <base>/name/<name>
GET <base>/service/<service>
~~~

响应可以直接返回 identity，也可以使用 code/msg/data envelope。

该 adapter 是 integration boundary，不定义 DID 协议本身。

---

## 4. Fee Verifier

基础接口：

~~~go
type FeeVerifier interface {
    VerifyFeeProof(
        recordHash [32]byte,
        keyHash [32]byte,
        namespace string,
        recordSize int,
        expiryHeight uint64,
        feeProof []byte,
    ) error
}
~~~

record-aware verifier 可以实现更高层接口直接读取完整 record。

### 4.1 FeeProof modes

当前 compact proof 类型：

~~~
FREE_LOCAL
AUTOPAY
ONESHOT
LEASE
~~~

核心语义：

- FREE_LOCAL：只在 endpoint policy 允许时接受；
- AUTOPAY：验证当前 contract/delegate/payment/capacity；
- ONESHOT/LEASE：只有部署了真实 verifier 语义时才可用于生产。

FeeProof 被 record signature 覆盖。

### 4.2 FREE_LOCAL

FREE_LOCAL 是服务节点本地策略，不是全网服务。

节点 policy 至少限制：

- max TTL；
- per-signer records/bytes；
- node-wide records/bytes；
- Blob key 数量。

配置通过：

~~~
GET /v3/dkvs/config
~~~

Wallet 不得自行硬编码一个 fallback TTL。

FREE_LOCAL 不进入 P2P。

### 4.3 AUTOPAY

AutopayFeeVerifier 通过 AutopayStateProvider 读取 contract state。

生产验证至少包括：

- template == autopay.tc；
- contract active 且未关闭；
- service/recipient/fee asset 匹配 policy；
- signer 派生 payer/delegate；
- delegate active；
- balance 足够；
- per-block amount 对应的 record capacity 足够。

容量按 delegate 隔离，不能互相消耗。

停止支付后，record 可以按 endpoint policy 短暂 retention，但不应因此产生 tombstone/delete history。

---

## 5. System Verifier

接口：

~~~go
type SystemVerifier interface {
    CanWriteSystem(key string, pubKey []byte) error
}
~~~

负责 /sys/* 写权限。

默认 deny-all。

外部治理模块必须决定：

- 哪些 signer 可以写；
- signer 的 key scope；
- rotation/revocation；
- future system record 的授权规则。

DKVS core 不持有治理私钥。

---

## 6. Checkpoint / snapshot boundary

DKVS checkpoint/snapshot 是本地当前状态计算结果，用于：

- 调试；
- 一致性检查；
- node-local administration。

它们不是：

- 全网 consensus root；
- 链上最终性证明；
- 自动签名的 /sys record。

当前版本不定义链上 anchor。

如果未来需要 anchor，必须作为单独协议设计，不在 DKVS core 中预建签名服务、重试队列或交易格式。

---

## 7. Wallet / CoreNode binding 与外部身份

钱包业务 KV RPC 的服务边界由当前签名 mapping 决定：

~~~
/account/<network>/<root-address>
~~~

AccountServiceDescriptor 中的 CoreNodeID 必须等于当前接收 RPC 的 CoreNode。

不再维护第二份本地 binding acceptance registry。

节点重启后直接从当前 mapping 恢复授权判断。

其他 CoreNode 可以通过 P2P 保存该 mapping，用于路由和解析，但 descriptor 指向谁，只有谁能接受对应钱包的普通业务 KV RPC。

---

## 8. 部署参数

这些属于部署配置，不应拆成另一份 open-decisions 文档。

### DID

生产环境必须明确：

- L1/L2 哪个 owner resolver 是最终权威；
- service DID 如何解析；
- network params；
- resolver endpoint / availability policy。

在未配置前保持 fail-closed。

### AUTOPAY

主网必须明确：

- global contract；
- service name；
- recipient；
- fee asset；
- full-record fee；
- miner/服务收益规则。

测试网参数不自动成为主网参数。

### System

主网必须明确：

- system signer/governance；
- allowed key scopes；
- rotation/revocation。

---

## 9. Runtime injection

部署可通过 Config 注入：

~~~go
dkvs.Config{
    Resolver:       realDIDResolver,
    FeeVerifier:    realFeeVerifier,
    SystemVerifier: realSystemVerifier,
    CurrentHeight:  chainHeightFunc,
}
~~~

embedded indexer 也可以通过 DKVSIntegrationConfig 或 runtime setters 注入。

传入 nil 应恢复保守默认：

- /name、/svc 不可写；
- /sys 不可写；
- 没有有效 fee proof 的普通网络写入拒绝；
- FREE_LOCAL 只有显式 node policy 允许时可用。

---

## 10. 不变量

外部集成不得破坏：

1. record 在远端存储前重新验证。
2. /name、/svc 每次新 mutation 验证当前 DID owner。
3. resolver/verifier unavailable 不 fail-open。
4. FeeProof 仍由 record signature 覆盖。
5. 外部模块不修改 P2P generation——P2P 本来就没有 generation。
6. 不引入 libp2p 第二套网络。
7. 不修改交易、区块、mempool、mining 或共识语义，除非单独协议升级。
8. 不因为外部 provider 需要历史数据而给 DKVS 增加 mutation log/delete history。
