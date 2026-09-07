# SatoshiNet 消息管理模块设计

日期：2026-08-30  
适用：`satoshinet`、`sat20wallet/sdk`  
状态：SatoshiNet 协议与服务端实现完成；Wallet SDK 提供协议接入与密码层；PWA IM/Topic UI 暂不接入

---

## 1. 目标与边界

消息管理模块基于现有 `MsgDKVSNotify` 和 DKVS，提供：

- 钱包账户之间的端到端加密 Direct Message；
- Topic 群组消息；
- 离线 mailbox；
- CoreNode 精准定向转发；
- SenderMsgID、幂等、ACK、重试；
- 免费 Direct、MESSAGE_SEND（仅 Topic Publish）与 TOPIC_SERVICE 计费接入；
- Topic membership、TopicKey、KeyPackage 与 key rotation。

模块不建立新的动态 P2P 路由协议，不使用广播寻找消息目标。

分层边界：

```text
P2P
    MsgDKVSNotify(Target) 精准传输

MessageManager
    account binding / SenderMsgID / billing / Direct / ACK / retry

TopicManager
    topic state / membership / publish permission / fan-out / pending delivery

TopicCryptoManager (wallet SDK)
    TopicKey / KeyPackage / AEAD / historical keys

DKVS
    canonical account binding
    AccountBound mailbox
    Topic Service CoreNode 本地 topic catalog
```

---

## 2. 术语

### Topic Owner

创建 Topic 的账户。

权限：

- 创建 Topic；
- 批准或拒绝 Join；
- Kick / Ban；
- 主动 Rotate TopicKey；
- 支付 Topic Service 费用。

### Topic Service CoreNode

运行该 Topic 的 CoreNode。

职责：

- 保存 Topic metadata/state/membership；
- 校验 membership control；
- 校验 Topic Publish；
- 按目标 CoreNode 聚合 fan-out；
- 保存 durable pending delivery；
- 协调 TopicKey rotation。

**Topic Service CoreNode 不保存 TopicKey 明文，不解密 Topic Message。**

### Topic Member

Topic 普通成员。拥有当前 TopicKey 的成员可以解密对应 KeySeq 的消息。

本文不再使用含义不明确的 `Topic Host`。

---

## 3. 固定网络路由

定向消息只支持三种路径：

```text
1. Target == local CoreNode
2. CoreNode -> direct peer CoreNode
3. CoreNode -> Bootstrap -> Target CoreNode
```

禁止：

```text
Target 不存在 -> P2P broadcast
```

Bootstrap 只做实时精准 relay：

- 不解密；
- 不保存长期消息；
- 不承担 mailbox；
- 不建立 route/circuit；
- Target 不在线时直接失败，由发送方 durable pending 重试。

不使用：

```text
valid_until_height
route lease
动态 circuit
DHT
```

---

## 4. `MsgDKVSNotify`

现有 `MsgDKVSNotify` 扩展：

```go
type MsgDKVSNotify struct {
    Target    string
    EventType uint8
    Data      []byte
}
```

其中：

```text
Target == ""
    普通 DKVS Notify

Target != ""
    精准 CoreNode 消息
```

消息应用统一使用：

```text
DKVSNotifyEventMessage
```

Direct、Topic、ACK、KeyPackage 不增加新的 P2P command。

`Data` 对 P2P 层完全 opaque。

---

## 5. Root Account Service Descriptor

### 5.1 稳定 key

每个网络、每个主账户只有一个免费公共控制记录：

```text
/account/<network>/<root-address>
```

value 是由主账户签名的 `AccountServiceDescriptor`，当前固定头包含：

```text
version
root AccountID
compressed CoreNode public key
capability bits
ordered bounded TLVs
```

它同时承担地址到主账户的映射、Account 到 CoreNode 的绑定和少量服务能力声明。
CoreNode ID 与现有 validator/CoreNode identity 使用同一表示，不增加另一套 node ID。

TLV 只允许协议定义的字段，类型必须严格递增且不能重复，单项和总 value 均有硬上限。
因此这个记录以后可以增加确实值得全网保存的小字段，但不能成为任意免费 blob。
未知 TLV 在钱包更新 CoreNode 或 capability 时必须原样保留。

### 5.2 绑定流程

Wallet 只连接 CoreNode，因此当前 `serverNode` 就是账户选择的服务 CoreNode：

```text
Wallet
  -> ordinary DKVS PUT of the root-signed descriptor
  -> receive PUT ACK and update local DKVS state
  -> BIND_ACCOUNT to current CoreNode

CoreNode
  -> verify root AccountID/address/signature
  -> verify descriptor CoreNodeID == this CoreNode ID
  -> persist local account-service acceptance
```

重新绑定或扩展 value 时继续更新同一个稳定 key，并使用更高 DKVS Seq。钱包只改自己
负责的字段并保留未知扩展。PUT ACK 已经对齐本地状态，不再额外下载 snapshot；后续 PUT
若冲突，再执行正常同步和重算。

### 5.3 解析

MessageManager 发送前通过 canonical binding 解析：

```text
AccountID -> current CoreNode ID
```

不得通过 HTTP host、Bootstrap 或本地猜测映射 CoreNode。

### 5.4 控制记录费用

`/account/<network>/<root-address>` 是窄范围、全网复制的账户控制记录，不进入普通用户
数据收费规则。

该例外只针对每个主账户在每个网络上的这一个稳定 key，不扩展成通用免费存储。
子钱包和子账户不创建自己的 mapping、binding、capability 或 mailbox identity；所有 DKVS
服务身份都由主账户统一管理。

---

## 6. `/mail` 与 AccountBound

正式 mailbox namespace：

```text
/mail
```

不存在 `/mailbox` alias。

### 6.1 Direct

```text
/mail/<recipient>/msg/<sender>/<message_id>
```

### 6.2 Topic Message

```text
/mail/<recipient>/topic/<topic>/msg/<sender>/<message_id>
```

### 6.3 Topic KeyPackage

```text
/mail/<recipient>/topic/<topic>/key/<key_seq>
```

### 6.4 Share

现有 owner-managed 数据继续使用：

```text
/mail/<account>/share/...
```

### 6.5 AccountBound 的准确含义

`AccountBound` 是**数据放置/复制策略**：

```text
/mail/<account>/...
    authoritative storage = account 当前绑定 CoreNode
```

它不等于写权限。

它也不改变 prefix 同步语义。钱包将整个信箱作为一个普通 managed prefix：

```text
/mail/<account>
```

该账户下任意 Direct、Topic、KeyPackage 或 Share 变化都会推进同一个
`PathMeta.EndpointGeneration`。钱包定期通过 `PrefixStatus` 检查该值，只有变化时才下载
该 endpoint 的 `PrefixSnapshot`。这里的 snapshot 是绑定 CoreNode 面向钱包提供的
endpoint-local prefix snapshot，不是 DKVS 节点间的 P2P/global snapshot。

对于 message entry：

```text
/mail/.../msg/...
/mail/.../topic/.../msg/...
/mail/.../topic/.../key/...
```

写入必须经过 MessageManager internal mailbox append，因为需要执行：

- sender signature；
- SenderMsgID；
- MessageID；
- 免费 Direct admission/rate limit；
- recipient binding；
- dedup；
- mailbox quota；
- ACK/retry。

因此普通 DKVS PUT 不能创建 message entry。

`/mail/.../share/...` 继续保留 owner-authorized 写入语义。

---

## 7. SenderMsgID 与 MessageID

### 7.1 SenderMsgID

每个账户与其当前绑定 CoreNode 之间维护发送序列：

```text
0, 1, 2, 3, ...
```

Direct 和 Topic Publish 共用同一序列。

例如：

```text
0 Direct -> B
1 Topic  -> developers
2 Direct -> C
3 Topic  -> bitcoin
```

一次 Topic Publish 无论多少成员，只消耗一个 SenderMsgID。

CoreNode 是 sequence authority：

```text
account -> next_sender_msg_id
```

SenderMsgID 只用于当前绑定关系下的顺序接纳。账户切换绑定 CoreNode 后，新 CoreNode 可以从 0 开始维护自己的 sequence；它不能作为跨 CoreNode、跨重绑的消息唯一标识。

### 7.2 MessageID

每条 Direct 或 Topic Publish 在 wallet 生成时创建独立、稳定的 MessageID：

```text
16 bytes / 32 lowercase hex
  = uint64 UnixMicroseconds (big endian)
  + 8 bytes cryptographic random entropy
```

MessageID 进入 sender 签名，并用于：

- mailbox immutable key；
- durable acceptance identity；
- Topic Publish 的 MESSAGE_SEND 计费幂等；
- ACK 关联；
- outbox 原消息重放。

同一个：

```text
SenderAccount + MessageID
```

重试：

- Direct 始终免费；Topic Publish 不重新计费；
- 不再次推进 sequence；
- 不产生第二条 mailbox entry。

Wallet 对需要可靠重试的发送保存本地 outbox，必须重发完全相同的已签名消息。

---

## 8. Direct Message

Wallet 构造：

```text
application payload
  -> recipient account ECDH encryption
  -> DirectMessage
  -> sender Schnorr signature
```

DirectMessage：

```text
SenderAccount
SenderMsgID
MessageID
RecipientAccount
Ciphertext
SenderSignature
```

发送流程：

```text
Wallet A
  -> CoreNode A
      validate binding
      validate sender signature
      validate Direct admission/rate limits
      validate SenderMsgID
      validate MessageID
      durable accept + next_sender_msg_id++
  -> Target CoreNode B
      validate recipient bound here
      internal mailbox append
      durable commit
  -> ACK
```

成功 ACK 的边界是：

> recipient mailbox 已经提交成功。

---

## 9. Mailbox outer/inner authentication

MessageManager mailbox 使用两层结构。

### Inner message

包含真实 sender 身份与签名：

```text
SenderAccount
SenderMsgID
MessageID
message-specific fields
Ciphertext
SenderSignature
```

### Outer DKVS mailbox record

由目标 CoreNode 内部生成：

```text
Key
Value = authenticated inner message
Seq = 1
IssueHeight
TTL
FeeProof = FREE_LOCAL（免费 Direct）
```

外层不伪装成 sender-signed DKVS record。

Wallet 读取消息时必须验证 inner sender signature。

---

## 10. Mailbox retention / quota

普通 message 与 Topic KeyPackage 分开计量。Direct 不引入消息专用缓存后端：

```text
recipient AUTOPAY active -> trusted internal persistent record, TTL = 0
recipient unpaid/expired -> standard DKVS FREE_LOCAL record, TTL > 0
AUTOPAY state unavailable -> retryable error, 不静默降级
```

免费 Direct 复用标准 FREE_LOCAL 的 TTL、过期清理、sender/global
记录数与字节数配额；`/mail` 的 AccountBound placement 仍保证记录不进入普通 P2P。
已有付费记录不会因后续付费过期而改写为 FREE_LOCAL，付费状态只决定新消息的 retention。

### Message

使用：

```text
MaxMsgTTL
MaxMsgSize
MaxMessages
MaxMsgBytes
MaxMessagesPerSender
MaxMsgBytesPerSender
```

### Topic KeyPackage

使用：

```text
MaxShareTTL
MaxShareSize
MaxShares
MaxShareBytes
```

KeyPackage 不占普通聊天消息 quota。

mailbox 满时返回 retryable ACK；发送方保留原始 MessageID，等待 TTL 清理释放容量后重试：

```text
Direct -> MAILBOX_FULL + retry_after
Topic  -> durable pending 保留，等待后续 retry
```

---

## 11. Mailbox delete

mailbox owner 可以删除自己的 entry。

流程：

```text
Wallet owner
  -> sign tombstone for exact mailbox key
  -> DELETE_MAILBOX
  -> bound CoreNode
  -> internal mailbox delete
```

Delete floor 必须保留。

因此已经删除的 immutable MessageID 即使收到延迟网络重试，也不能被重新创建。

---

## 12. Topic Service state

Topic Service CoreNode 保存：

```text
/topic/<topic>/meta
/topic/<topic>/state
/topic/<topic>/members/<account>
```

Topic 正文不保存在 `/topic`。

`TopicMeta` 至少包含：

```text
TopicName
DisplayName
OwnerAccount
ServiceCoreNode
JoinPolicy
MessagePolicy
MaxMembers
```

`TopicState`：

```text
Status
KeySeq
MemberCount
LastCommitHash
```

`LastCommitHash` 用于 membership/key rotation exact replay 幂等。

---

## 13. Topic 创建

Topic 创建必须由 Owner 签名：

```text
TopicCreateRequest
  Meta
  OwnerSignature
```

并且：

```text
Meta.OwnerAccount 必须绑定当前 CoreNode
Meta.ServiceCoreNode 必须等于当前 CoreNode
```

初始状态：

```text
KeySeq = 1
Owner = ACTIVE
MemberCount = 1
```

初始 TopicKey 由 Owner wallet 本地生成并持久化；CoreNode 不接触 TopicKey。

相同 Topic Owner + 相同 metadata 的 Create 重试是幂等的。

---

## 14. Topic membership 状态

成员状态：

```text
ACTIVE
PENDING_JOIN
PENDING_LEAVE
LEFT
BANNED
```

有效 key holder：

```text
ACTIVE
PENDING_LEAVE
```

可以 publish：

```text
ACTIVE only
```

因此成员一旦发出 Leave，即使新 key 尚未完成 rotation，也立即失去发布权限。

消息 fan-out 只包含 `ACTIVE` 成员；`PENDING_LEAVE` 立即停止接收新消息。它仍被计入当前 key-holder 集合，仅用于精确校验下一次 REMOVE rotation 的 KeyPackage recipient set，因为它在 rotation 完成前仍知道旧 TopicKey。

---

## 15. Join

Join 必须由 **Topic Owner 审批**。

### 15.1 申请

```text
Applicant wallet
  -> signed JOIN request
  -> Applicant bound CoreNode
  -> Topic Service CoreNode
```

Service CoreNode 只执行：

```text
member -> PENDING_JOIN
```

此时：

```text
KeySeq 不变
MemberCount 不变
Applicant 没有当前 TopicKey
Applicant 不能 publish
```

### 15.2 Owner approve

Owner wallet：

1. 读取 Topic state；
2. 生成新的随机 TopicKey；
3. `NewKeySeq = CurrentKeySeq + 1`；
4. 对变更后的所有合法 key holder 生成 KeyPackage；
5. 签署 `ADD` membership commit；
6. 提交 Topic Service CoreNode。

Service CoreNode 验证：

```text
Issuer == Topic Owner
target == PENDING_JOIN
BaseKeySeq == current
NewKeySeq == current + 1
KeyPackage recipient set == post-change key-holder set
所有 KeyPackage signature 正确
```

成功后：

```text
target -> ACTIVE
KeySeq++
MemberCount++
fan-out KeyPackages
```

### 15.3 Owner reject

Owner 可以提交签名的 Join rejection：

```text
PENDING_JOIN -> LEFT
```

因为申请人从未获得当前 TopicKey，所以拒绝 Join **不需要 rotation，不推进 KeySeq**。

---

## 16. Leave

Leave **不需要 Topic Owner 审批**。

### 16.1 Leave request

成员自己签名：

```text
ACTIVE -> PENDING_LEAVE
```

立即效果：

```text
不能再 publish
不能再接收新的 Topic fan-out
```

但在新 TopicKey 提交前，该成员仍知道旧 TopicKey，因此仍属于当前 key holder。

### 16.2 完成 rotation

新的随机 TopicKey 必须由一个**仍为 ACTIVE 的成员 wallet**生成。

提交 `REMOVE` membership commit：

```text
Issuer = remaining ACTIVE member
Target = PENDING_LEAVE member
KeyPackages = 所有 post-change key holders
```

不要求 Issuer 是 Topic Owner。

Service CoreNode 验证后：

```text
PENDING_LEAVE -> LEFT
KeySeq++
MemberCount--
```

新 KeyPackage 不包含离开成员。

因此：

> Leave 的权限来自成员自己的 Leave signature；后续 rotation 是密码学协调，不是 Owner 审批。

首期 Wallet SDK 的便捷 rotation API要求 coordinator 当前连接 Topic Service CoreNode，以便同步确认 commit 结果。通常由 Owner 承担协调；这不改变协议层的“Leave 无需 Owner 批准”。SatoshiNet 服务端允许其他 remaining ACTIVE member 提交 REMOVE commit。

---

## 17. Kick / Ban / manual rotate

### Kick

```text
Issuer = Topic Owner
Target != Owner
Target current key holder
-> LEFT
-> KeySeq++
```

### Ban

```text
Issuer = Topic Owner
Target != Owner
Target current key holder
-> BANNED
-> KeySeq++
```

### Manual Rotate

```text
Issuer = Topic Owner
membership unchanged
KeySeq++
```

以上都必须携带完整 post-change KeyPackage 集合。

---

## 18. Membership commit 原子边界

有效 commit 必须满足：

```text
BaseKeySeq == current KeySeq
NewKeySeq == current KeySeq + 1
```

并包含：

```text
exact(post-change key holders) == exact(KeyPackage recipients)
```

禁止：

- 少发 KeyPackage；
- 多发 KeyPackage；
- 给已移除成员发新 key；
- duplicate recipient；
- 使用同一 KeySeq 提交另一套 TopicKey。

提交顺序：

```text
verify authorization/signatures
-> durable stage KeyPackage deliveries
-> persist membership + KeySeq + LastCommitHash
-> fan-out KeyPackages
```

如果 catalog persist 失败，必须回滚本次 staged delivery。

### Exact replay

`LastCommitHash` 保存完整 canonical membership commit hash。

如果 HTTP response 丢失：

```text
same commit + same NewKeySeq + same LastCommitHash
    -> idempotent success

different commit + same NewKeySeq
    -> reject
```

---

## 19. TopicCryptoManager

Wallet SDK 提供 `TopicCryptoManager`。

职责：

```text
TopicKey generation
TopicKey local encrypted persistence
historical TopicKey retention
KeyPackage create/decrypt
Topic message encrypt/decrypt
sender signature
key rotation preparation
```

### 19.1 本地 TopicKey

TopicKey 为随机 32 bytes。

本地 DB 中不裸存 TopicKey；使用账户自己的 account ECDH encryption 再包一层。

旧 KeySeq 的 key 保留，用于仍存在的历史 mailbox message。

### 19.2 KeyPackage

对每个成员：

```text
EncryptedTopicKey = account-ECDH Encrypt(recipient, TopicKey)
```

KeyPackage：

```text
Recipient
EncryptedTopicKey
IssuerSignature
```

签名 context 绑定：

```text
TopicName
KeySeq
IssuerAccount
Recipient
EncryptedTopicKey
```

### 19.3 Topic Message crypto

每条消息派生独立 message key：

```text
MessageKey = KDF(
    TopicKey,
    TopicName,
    KeySeq,
    SenderAccount,
    SenderMsgID,
    MessageID
)
```

当前 SDK 使用 AES-GCM。

AAD 同样绑定：

```text
TopicName
KeySeq
SenderAccount
SenderMsgID
MessageID
```

Topic Publish 另外由 sender Schnorr signature 认证。

---

## 20. Topic Publish

Wallet：

```text
plaintext
 -> TopicCryptoManager
 -> one ciphertext
 -> sender signature
 -> current CoreNode
```

Topic Publish：

```text
TopicName
KeySeq
SenderAccount
SenderMsgID
MessageID
Ciphertext
SenderSignature
```

Sender CoreNode：

- 校验 sender bound here；
- 校验 signature；
- 校验 SenderMsgID；
- MESSAGE_SEND admission；
- durable accept；
- 转发 Topic Service CoreNode。

Topic Service CoreNode：

- Topic Service active；
- sender == ACTIVE member；
- KeySeq == current；
- sender signature valid；
- durable stage fan-out；
- ACK sender CoreNode；
- 执行 member delivery。

PENDING_LEAVE / LEFT / BANNED 均不能 publish。

Topic Service 拒绝一个签名有效的 Publish 时必须返回明确 ACK，不能让 sender CoreNode 的 durable pending 永久悬挂。

---

## 21. Topic fan-out

设：

```text
N = members
C = distinct recipient CoreNodes
M = ciphertext size
```

sender 只产生一份 ciphertext。

Topic Service CoreNode：

```text
member account
 -> bound CoreNode
 -> group by CoreNode
```

正文网络复杂度约为：

```text
O(C * M)
```

不是：

```text
O(N * M)
```

目标 CoreNode 收到 fan-out 后，为本节点每个 recipient 生成 AccountBound mailbox entry。

同一 CoreNode 的多个 recipient 共享同一份 network ciphertext。

---

## 22. Topic Publish ACK / pending

Sender 成功边界：

> Topic Service CoreNode 已经将 fan-out 纳入 durable pending delivery。

不等待所有成员 CoreNode 成功。

目标 CoreNode mailbox 成功后 ACK 对应 fan-out delivery。

只有成功 ACK 删除 pending。

重试前重新解析 recipient 的 Account -> CoreNode binding，并在必要时重新分组 batch。

---

## 23. Topic Service control API

当前统一通过：

```text
/message/service
```

支持：

```text
BIND_ACCOUNT
NEXT_MESSAGE_ID
SEND_DIRECT
DELETE_MAILBOX
CREATE_TOPIC
TOPIC_STATE
TOPIC_JOIN
TOPIC_LEAVE
TOPIC_REJECT_JOIN
TOPIC_MEMBERSHIP_COMMIT
TOPIC_PUBLISH
```

Transcend/STP HTTP 层只转发 opaque JSON，不复制消息协议授权逻辑。

所有签名、membership、sequence、billing、routing 都在 SatoshiNet 内完成。

---

## 24. Wallet SDK 接入层

SDK 当前提供：

### Direct / Offline / RGB11

```text
BindAccountToCurrentCoreNode
SendAccountDirectMessage
RetryAccountDirectMessage
ReadAccountDirectMessages
DeleteMailboxMessage
SendOfflineMessage
ReadOfflineMessages
```

RGB11 address delivery / ACK 已迁移到 MessageManager Direct transport。

### Topic

```text
CreateMessageTopic
GetMessageTopicState
RequestMessageTopicJoin
RequestMessageTopicLeave
RejectMessageTopicJoin
ApproveMessageTopicJoin
FinalizeMessageTopicLeave
KickMessageTopicMember
BanMessageTopicMember
RotateMessageTopicKey
PublishMessageTopic
RetryMessageTopicPublish
SyncMessageTopicKeys
ReadMessageTopicMessages
```

### Durable client state

SDK 对以下操作保存本地 outbox：

```text
Direct Message
Topic Publish
Topic membership/key rotation commit
```

rotation outbox 中的新 TopicKey 使用账户自身 ECDH 加密保存。

只有 Service CoreNode 确认 commit 后，新的 key 才提升为本地 current TopicKey。

---

## 25. PWA 边界

本阶段：

```text
PWA 不增加 IM UI
PWA 不增加 Topic UI
PWA 不增加 Topic membership 交互页面
```

协议与 SDK 能力先独立完成并通过测试。

未来 PWA 接入只调用 SDK，不重新实现：

- 消息签名；
- Topic crypto；
- SenderMsgID；
- MessageID；
- membership state machine；
- CoreNode routing。

---

## 26. 计费

### Direct

Direct 始终免费，包括承载 RGB consignment 和 RGB ACK 的 Direct。不引入
`ApplicationReceipt`；ACK 是普通免费 Direct。

CoreNode 在 sequence acceptance 和 durable write 之前执行内存限流，至少覆盖 sender、
sender-recipient pair、recipient inbound 和 node total，并同时统计消息数与字节数。被拒绝时返回
`DIRECT_RATE_LIMITED + retry_after_ms`，不推进 SenderMsgID、不写 acceptance/pending/mailbox。

### MESSAGE_SEND

由 Topic Publish sender 支付。一个正式接受的 Topic Publish 消耗一次 usage。

以下不重复计费：

```text
network retry
ACK retry
Topic fan-out
same MessageID replay
```

### TOPIC_SERVICE

由 Topic Owner 维持 Topic Service Autopay。

不修改现有 Autopay 共识格式。

Direct recipient 的 AUTOPAY 只购买自己信箱中新到消息的持续保存能力，不是发送费用。

---

## 27. 安全不变量

必须长期保持：

1. Bootstrap 不解密消息。
2. CoreNode 不解密 Direct payload。
3. Topic Service CoreNode 不保存 TopicKey。
4. Topic Service CoreNode 不解密 Topic Message。
5. `/mail` 是普通 prefix，必须提供标准 endpoint PathMeta status/snapshot；具体记录是否参与 P2P relay/checkpoint 由其存储模式决定，FREE_LOCAL 消息只保存在绑定 CoreNode。
6. 普通 DKVS PUT 不能创建 message entry。
7. Message author 由 inner signature 确定，不依赖 outer DKVS signer。
8. CoreNode 与 BootstrapNode 作为受信路由节点，不增加额外节点签名；但接收 CoreNode 必须从 inner data 解出 author，并要求其 DKVS canonical binding 等于 notify 的 SourceCoreNode。
9. ACK 只能由该 durable acceptance 当前记录的目标 CoreNode 返回；MessageID、SenderMsgID 与 sender 必须全部匹配。
10. SenderMsgID 在账户与当前绑定 CoreNode 之间从 0 严格递增。
11. Direct 与 Topic Publish 共用该绑定关系下的 SenderMsgID。
12. MessageID 是独立、已签名、跨重绑稳定的消息标识，不能由 SenderMsgID 替代。
13. retry 不重复计费、不推进 SenderMsgID。
14. Join 必须 Topic Owner approve。
15. Leave 不需要 Topic Owner approve。
16. PENDING_LEAVE 立即失去 publish 和新消息接收权限。
17. 被移除成员不能获得新 TopicKey。
18. 新成员默认不能获得加入前 TopicKey。
19. KeyPackage recipient 集合必须精确匹配新 key-holder 集合。
20. 同一 KeySeq 不允许出现两套不同 membership commit / TopicKey。
21. Topic 正文只生成一份 ciphertext。
22. Topic 历史不集中存储在 `/topic`。
23. Target 不存在时绝不广播。
24. 不引入 `/mailbox` alias。
25. 不引入 `valid_until_height`。

---

## 28. 核心测试基线

当前实现覆盖：

### Routing / Direct

- local / direct / Bootstrap relay；
- missing target 不广播；
- Direct inner signature；
- author binding 与 notify source 一致；
- ACK source/message identity 校验；
- mailbox commit-before-ACK；
- dropped ACK retry；
- recipient CoreNode migration；
- Direct 无 usage；Topic same MessageID only one usage；
- Direct FREE_LOCAL/paid retention selection；
- sender/pair/recipient/global rate limit and retry-after；
- acceptance cache follows FREE_LOCAL TTL；
- persistent sender acceptance across restart。

### Account binding

- canonical stable binding key；
- binding signature；
- current CoreNode acceptance；
- rebind；
- resolver restart。

### RGB11

- Address consignment 经 MessageManager；
- large payload；
- ACK；
- SenderMsgID transport sequence；
- MessageID transport identity；
- transfer ID 作为 application identity；
- legacy read compatibility。

### Topic

- Owner-approved Join；
- Join rejection without rotation；
- unilateral Leave；
- non-owner remaining ACTIVE member REMOVE commit；
- PENDING_LEAVE publish rejection；
- complete KeyPackage set；
- exact commit replay；
- same KeySeq different commit rejection；
- 1000 members / 20 CoreNodes fan-out；
- fan-out batch splitting；
- durable pending-before-ACK；
- recipient CoreNode regroup on retry；
- key delivery retry；
- Topic catalog restart；
- multi-CoreNode + Bootstrap control path；
- `/message/service` Create/State/Join/Commit/Publish path。

### SDK crypto/client

- TopicKey encrypted local storage；
- KeyPackage encrypt/decrypt/signature；
- historical TopicKey retention；
- Topic AEAD；
- ciphertext/package tampering rejection；
- Topic control outbox；
- Create retry initial-key stability；
- Topic Publish starts at SenderMsgID=0 and uses shared sequence。

---

## 29. 最终协议结构

```text
Wallet A
    |
    | encrypted + signed application message
    v
Bound CoreNode A
    |
    | sender sequence + billing + durable acceptance
    |
    +---- direct target --------------------------+
    |                                             |
    +---- Bootstrap ---- target CoreNode ----------+
                                                  |
                                                  v
                                        AccountBound /mail
```

Topic：

```text
Member Wallet
   |
   | one Topic ciphertext
   v
Bound CoreNode
   |
   v
Topic Service CoreNode
   |
   | group recipients by bound CoreNode
   +---- CoreNode B -> /mail/B-members/...
   +---- CoreNode C -> /mail/C-members/...
   +---- CoreNode D -> /mail/D-members/...
```

TopicKey 永远留在 wallet crypto domain。

该文档作为当前 SatoshiNet MessageManager / TopicManager 与 Wallet SDK 接入层的实现基线。PWA 的 IM/Topic UI 不属于本阶段交付范围。
