# DKVS 最终设计

更新时间：2026-10-04  
适用仓库：satoshinet、sat20wallet/sdk  
文档性质：**DKVS 唯一规范性设计文档**

> DKVS 采用“当前状态”模型。没有 mutation log、持久 tombstone、delete floor、全网 generation、全网 sequencer，也不保存 prefix 的永久 source。  
> generation 只属于 Wallet/PWA 与当前服务节点之间的同步和写入校验；**P2P 不传播 generation**。  
> 当前功能尚未正式发布，不保留旧协议兼容、双写或迁移分支。

---

## 1. 目标与边界

DKVS 是面向 SatoshiNet 应用的小数据分布式 KV，目标是：

- owner/authority 控制写入；
- record 可独立验证；
- 单 key 严格 revision；
- Wallet/PWA 在绑定 CoreNode 上获得低成本、可验证的本地 confirmed replica；
- CoreNode/Bootstrap/普通节点通过 P2P 实时传播和当前集合同步最终收敛；
- FREE_LOCAL、AUTOPAY、PAID 使用同一套 KV 语义，仅放置/费用策略不同。

DKVS 不解决：

- 业务字段自动 merge；
- 跨账户分布式事务；
- quorum/BFT/leader election；
- CRDT；
- 历史 mutation 查询；
- 删除历史证明；
- 全网可比较的 revision/generation。

核心原则：

~~~
record 的新旧：看这个 key 自己的 Seq / IssueHeight / RecordHash
Wallet freshness：看当前服务节点的 endpoint-local generation
P2P convergence：看签名 record + 完整当前集合/root
~~~

---

## 2. Key、Collection 与权限

### 2.1 主要 namespace

| Namespace | 示例 | 写入控制 | 网络放置 |
| --- | --- | --- | --- |
| /account | /account/<network>/<root-address> | root account | 全网复制控制记录 |
| /personal | /personal/<account>/<module>/... | account owner | FREE_LOCAL 或网络存储 |
| /blob | /blob/<account>/<blob-key> | account owner | FREE_LOCAL 或网络存储 |
| /name | /name/<did> | 当前 DID owner | 网络 |
| /svc | /svc/<did>/... | 当前 DID owner | 网络 |
| /mail | /mail/<account>/... | MessageManager / owner | AccountBound |
| /topic | Topic service state | Topic service policy | service-local / protocol-defined |
| /sys | system records | SystemVerifier | 网络 |
| /tmp | temporary data | signer/local policy | LocalOnly |

Collection 是同步、generation 和当前集合 root 的基本范围。不同 key 的 Seq 永远互不相关。

### 2.2 Account binding

稳定控制 key：

~~~
/account/<network>/<root-address>
~~~

value 为 root account 签名的 AccountServiceDescriptor，至少包含：

~~~
AccountID
CoreNodeID
capability bits
bounded protocol TLVs
~~~

**当前签名 binding record 本身就是唯一绑定事实。**

不再保存第二份“本 CoreNode 已接受该 binding”的本地 acceptance 状态。

Wallet 普通业务 KV RPC 必须满足：

~~~
当前 /account mapping 的 CoreNodeID == 本 CoreNode
~~~

其他 CoreNode 即使通过 P2P 保存了该 mapping，也不能替代目标 CoreNode 接受钱包业务写入。

/account 的独立 binding 创建/刷新是唯一未绑定 bootstrap 写例外；其 descriptor 必须明确指向当前 CoreNode。

---

## 3. DKVSRecord

~~~go
type DKVSRecord struct {
    Version     uint32
    Key         string
    Value       []byte
    PubKey      []byte
    Signature   []byte
    Seq         uint64
    IssueHeight uint64
    TTL         uint64
    FeeProof    []byte
    Flags       uint32
}
~~~

record 中不存在：

~~~
generation
source node
expiry time
mutation id
delete history
~~~

### 3.1 Seq

正常业务写入：

~~~
不存在 key：Seq = 1
存在 key：Seq = current.Seq + 1
~~~

删除命令：

~~~
Seq = current.Seq + 1
并签名绑定被删 current RecordHash
~~~

删除完成后 key 物理不存在。之后重建：

~~~
Seq = 1
IssueHeight = 当前高度（钱包 RPC 允许 current / -1 / -2）
~~~

节点不得因为过去曾存在更高 Seq 而阻止新的生命周期。

### 3.2 IssueHeight / TTL

~~~
expiry_height = IssueHeight + TTL
~~~

- TTL > 0：有限租期；
- TTL = 0：没有固定 record TTL，通常由 AUTOPAY/PAID policy 决定；
- Wallet 新业务写入的 IssueHeight 必须位于当前服务节点高度的短窗口：

~~~
currentHeight - 2 <= IssueHeight <= currentHeight
~~~

该短窗口只约束**新 Wallet RPC 写入**。P2P 同步旧但仍有效的当前记录不能因为原始 IssueHeight 较早而被拒绝。

### 3.3 删除不是状态

删除在 wire/签名层可以使用 FlagTombstone 表示“删除操作”，但：

- 不保存为当前 record；
- 不进入 snapshot；
- 不形成 delete floor；
- 不进入 StateRoot；
- 不作为永久防重放记录。

提交后原子删除：

~~~
record row
record hash index
该 key 的最新 change-generation 索引
~~~

离线节点错过删除，通过下一次完整当前集合校准发现 key 缺失。

---

## 4. Generation：只用于 Wallet ↔ 服务节点

### 4.1 定义

服务节点对每个 endpoint-visible collection 维护本地 EndpointGeneration。

只要该 endpoint 上这个 collection 的可见当前状态变化，就推进 generation，包括：

- create/update/delete；
- FREE_LOCAL；
- AUTOPAY/PAID 当前可见性变化；
- expiry/prune；
- AccountBound 数据变化。

服务端还可记录每个当前 key 最近一次变化对应的本地 generation，用于增量分页。

**generation 不是全网版本号。**

禁止：

~~~
比较两个 CoreNode 的 generation 大小
把 generation 放入 DKVSRecord
把 generation 放入 P2P Notify
把 generation 放入 P2P prefix snapshot
通过 P2P 中继传播 generation
~~~

### 4.2 Wallet confirmed replica

Wallet/PWA 本地持久化：

~~~
EndpointID
managed prefix
已完成同步的 generation
已确认 current records
outbox（与 confirmed replica 分离）
~~~

本地 generation 只对同一个 EndpointID 有效。

### 4.3 首次/恢复同步

如果 Wallet 没有某 prefix 的已确认 generation：

~~~
full current-set sync
→ 验证 root
→ 原子安装 records
→ 保存 endpoint generation
→ scope READY
~~~

持久化旧缓存可以展示为 cache，但首轮同步完成前不能作为“最新状态”生成写入。

---

## 5. Wallet 写入协议

### 5.1 不存在 /write-context

Wallet 新 PUT **不先请求服务端 write context**。

SDK 直接使用本地 confirmed replica 已保存的 prefix generation。

如果本地没有 generation：

~~~
先同步 prefix
→ 再构造写入
~~~

### 5.2 Request authorization

普通业务 batch-CAS 请求包含：

~~~
EndpointID
RequestID
mutations
CAS preconditions
prefix + local generation
wallet request signature
~~~

request signature 覆盖：

~~~
EndpointID
RequestID
ordered prefix generations
每个 mutation 的 RecordHash
CAS: expected current hash 或 expect_absent
~~~

record 自身签名证明 record author；request signature 证明钱包**本次**授权了这些 CAS 条件和 generation。

因此，第三方仅复制一个历史签名 record，不能自行构造新的 expect_absent 或 CAS 请求来恢复已删除数据。

### 5.3 服务端提交检查

服务端在实际 commit 锁内重新验证：

1. 请求 signer；
2. 当前 /account binding 指向本 CoreNode；
3. client generation == 本 endpoint 当前 generation；
4. CAS；
5. key-local Seq；
6. IssueHeight 短窗口；
7. namespace / DID / fee / quota；
8. batch 全部通过后原子提交。

generation 不一致返回 STALE_GENERATION。

客户端处理：

~~~
同步 prefix
→ 基于新的 confirmed current state 重新执行业务 mutation
→ 重新生成 record / CAS / request signature
→ 新 RequestID 提交
~~~

**不能只给旧请求换一个新 generation 后重发。**

### 5.4 Outbox 与 ACK

网络结果未知时，outbox 保存**原始完整已签名请求授权**。

重试必须重发完全相同的请求，不重新获取 generation、不重新授权历史 mutation。

写 ACK 只表示该请求的服务端处理结果：

- 验证 response；
- 完成/更新 outbox；
- 不写 confirmed KV；
- 不删除 confirmed KV；
- 不推进已完成同步 generation。

confirmed replica 只由同步/Watch 安装路径更新。

账户后台任务与 mailbox 维护分别保留和返回错误，不决定通过自身来源、完整性和基线校验的 managed 接收是否继续；沿用同一 worker 和已有任务保留规则。任务轮次保留失败项并汇总错误，其余任务继续执行，包括已确认变更的通知；各任务仍验证自身依赖，不因其他任务失败丢失通知或停止接收。

发送与接收分别返回结果。原请求发生发送冲突、签名高度失效、资金暂停或临时网络错误时，同 endpoint 的接收校验通过后仍继续 sync / Watch；临时发送失败通过已有 5 秒重试间隔重放原授权，Watch 在此期间仍接收。接收本身的 endpoint、基线及完整性校验不能绕过。明确 record TTL 过期返回 `DKVS_EXPIRED_RECORD`，清理被拒绝的 outbox，不修改 TTL 或重新签名。明确的业务拒绝返回错误，保留原请求与拒绝信息并停止自动提交；HTTP 429 等临时失败仍重试原请求。协调器通过 defer 在返回或 panic 时释放占用并唤醒等待者；本地不可恢复错误继续 fail-fast。

---

## 6. Wallet 同步协议

### 6.1 Active sync

主要接口：

~~~
POST /v3/dkvs/active/sync
POST /v3/dkvs/active/watch
~~~

ActiveScope 可以是一个 managed prefix，也可以收窄到指定 keys。

同步 metadata：

~~~
EndpointID
scope
endpoint-local generation
current-set root
view height
~~~

full sync：

- 返回完整当前集合，支持分页；
- 每页必须固定在同一个 generation + root + view height；
- 中途服务端状态变化则旧 cursor 失效，客户端重新开始；
- 完成后本地原子替换该 scope。

incremental sync：

- 客户端提交上次完成的 generation；
- 服务端返回该 generation 之后仍然**当前存在**的变化记录；
- 不是 mutation log；
- 删除不会作为历史 tombstone 返回；
- 如果 current root 不能通过增量结果解释，客户端转 full sync。

### 6.2 Watch

/active/watch 是 long-poll：

- 服务端不保存终端 session；
- 不保存 offline event queue；
- 不保存 mutation log；
- 每次最多返回一个发生变化的 scope 的当前 page；
- 慢消费者下一次直接读取最新当前状态。

已完成的 Watch page 可能在前台写入 ACK 后才进入安装。页面落后于本地 ACK 下界时先拒绝该页面，再通过已有有界同步循环重新读取当前状态；新响应仍严格验证 endpoint、ACK 下界、root 和安装 baseline。验证高度取成功安装后的 metadata，不使用已丢弃旧页面的高度。

这使 PWA 可以像轻节点一样在线接收自己关心的数据，而服务端只按订阅 scope 过滤。

### 6.3 按需读取

只保留 `POST /v3/dkvs/prefixes/read` 用于有界、无同步状态的按需读取。
managed replica 统一使用 Active sync/watch；不保留旧 status/snapshot/delta 兼容接口。

SDK 未订阅的 key / prefix 按需在线读取，超时 5 秒，不保留 unmanaged 短期缓存。managed confirmed replica 与 Watch 保留，已订阅数据的离线副本不受该缓存清理影响。待充值 outbox 保留原授权与提示，但不阻断订阅接收；现有 wake 后只能重放原请求，不新增充值轮询任务。

按需单记录与显式权威读取沿用一次 5 秒 deadline，覆盖 key-state、record 和 best-height 查询。选定恢复来源或其他按需来源的高度仅用于校验本次读取，不写入 Manager 的共享校验高度。刷新共享高度前复用 EndpointID 与持久副本来源检查，来源不一致返回 endpoint mismatch；存储策略/容量估算不混用另一来源策略与本地高度。来源检查先于网络刷新和高度回退；同来源临时失败仍可使用已有可信高度，不能借网络失败忽略已知来源不匹配。零高度属于已知高度，签名与 TTL 校验保持。

同 endpoint、同一完整目录组的刷新保留最后确认副本的读取资格，网络失败标记 OFFLINE_READY，成功恢复 READY。首次同步、不完整目录组、新增 prefix 和来源切换不能提升为离线可读。collection Sync/Watch 的 ViewHeight 只能推进已知 endpoint 过期校验高度；主动 best-height 查询保留确认真实链回退的职责。

SDK 的 Get/List、就绪检查与使用本地状态的写入 builder 共享确认边界：全部已注册或待同步目录均完成同步，client 已知的 EndpointID 与持久副本来源一致。已知另一来源返回 endpoint mismatch，保留旧副本；同身份 URL 别名可共用副本。未知身份的离线读取仍归属于副本已持久化的 EndpointID，不重新归属给未知来源。

Manager 重建后，只有合格确认副本可用其持久 ViewHeight 恢复尚未知的校验高度，零高度也视为已知；已有在线高度优先。Get/List 执行一致的签名与 TTL 校验。恢复的高度是最后确认边界，不推造离线期间的最新链高。

账户 managed-data 等待只观察本地确认 state/blob 及其与 profile 的内容、版本和摘要关系，未就绪时唤醒已有 worker，不自行发起或等待同步 HTTP。调用方 context 覆盖整个等待，包括根钱包暂不可用阶段；无 deadline 时最外层只建立一次 30 秒默认截止时间。取消等待不取消共享接收 worker，不额外启动 worker。

SDK Blob 统一使用 `DKB1 + big-endian uint32(metadata length) + metadata + data`，metadata 允许为空，data 非空。固定 8 字节封装计入 Blob value 上限，不压缩或改写应用 data。解码只接受该格式，不猜测旧 raw 内容，也不自动改写存量 record；普通 record API 继续返回存储字节。

---

## 7. P2P：没有 generation

### 7.1 基本原则

P2P 只传播：

~~~
签名 KV operation
当前集合 snapshot
captured current-set root / pagination cursor
snapshot 创建时的校验高度（只用于记录有效性验证）
~~~

**P2P 不传播任何 generation。**

### 7.2 节点启动/重连

READY 是节点级状态：所有已发现和排队的目录完成 snapshot 安装，并按到达顺序处理完缓存 Notify 后，节点才进入 realtime。单个连接或目录完成不能开放其他目录的 Notify。

流程：

~~~
连接有效 CoreNode / Bootstrap
→ 从 signed generic 分页提取、去重目录线索，与本地目录合并后加入节点级统一队列
→ 同一时刻只有一个 source/session 执行目录同步
→ sync 期间收到任意有效 source 的 Notify：只放入节点内存 pending queue
→ 所有目录 snapshot 原子安装成功
→ 按到达顺序处理 pending Notify，期间的新 Notify 继续入队
→ 在同一个入队锁内确认队列为空并设置节点 READY
→ 进入 realtime
~~~

pending queue：

- 仅节点运行期内存，连接断开不丢失待同步目录；
- 不持久化；
- 有数量/字节上限；
- 溢出时不猜测，重新同步。

目录队列复用现有去重和重试机制：失败或断连只结束当前传输，不把目录记为已完成；剩余任务由有效连接继续执行。请求超时和重连沿用现有调度，不新增持久任务表或 source ownership。

CoreNode/Bootstrap 作为有效 source 的资格来自节点身份/配置；不要求对端设置旧的 SFNodeMiner 标志。

### 7.3 Sync session source

**只有一次 prefix/current-set sync session 才固定 source。**

- 一个 session 的分页、root 和签名必须来自同一个 source；
- session 完成后不保存“这个 prefix 永久属于该 source”；
- 不保存 source generation；
- 不跨 source 比较任何 revision。

P2P snapshot 的分页边界使用：

~~~
offset
captured snapshot root
~~~

source 在 session 的首个请求取得一次独立 snapshot，所有分页只读取该 snapshot，不再逐页重新查询当前集合。传输期间链高度前进或 source 发生新的写入，都不改变已捕获的 snapshot；目的节点通过缓存的实时 Notify 在安装后继续追上新状态。

同一活动 session、相同 filters 的首个请求重发复用该 snapshot；同 session 不允许改变 filters。接收端仅保留前一已验签响应的摘要，忽略内容和签名完全一致的重复页，不重复安装或取消下一页计时器。消息在连接内顺序处理和发送，不保存响应页历史；结束、取消或新 session 清除摘要。其他 session、错误签名和不同内容仍走原校验。

`ViewHeight` 保留为 snapshot 创建时的固定校验高度，用于 TTL、IssueHeight 等记录有效性验证；它不是分页游标，也不与 source 当前链高度逐页比较。root/source/signature、scope、权限、fee 和目的节点本地并发安装保护仍需验证。

每个 source 连接至多保留一个 snapshot，沿用当前目录 snapshot 的 64 MiB records 上限；完成、取消、断连或闲置超时（当前 2 分钟）释放。分页直接读取已捕获 records 的对应区间，不为每页重新构造整个目录。

目录发现不安装 records，也不要求所有发现页拥有同一全局 root；接收端仅保留去重 collection paths，不暂存发现集合的完整 records。发现不是同一瞬间的全局目录清单，新目录由 Notify 或下一轮 anti-entropy 补齐。各目录的固定 snapshot、完整 root / 权限 / fee / 安装基线校验保持。

沿用现有发现消息仍会携带 records，当前清理减少完整暂存，不宣称已消除重复网络传输。每个待响应页向同一 source 共尝试两次，每次超时 10 秒；仍超时后交回现有连接选择，优先其他有效 source，没有替代 source 时重试原源。目录队列与节点未 READY 状态保留，不关闭其他链协议使用的连接。发现页校验失败仍严格拒绝，但空 path 的未完成发现任务也进入既有换源回调。

### 7.4 Realtime Notify

实时 Notify：

- 只要求来源是有效 CoreNode/Bootstrap；
- 不要求等于上一次 sync source；
- 不要求是 Wallet 绑定 CoreNode；
- payload 只是原始签名 DKVS operation，没有 generation。

`serveFilters` 只描述当前同步 session 的临时范围；`notifyFilters` 描述连接持续订阅。仅完成的 discovery/订阅声明替换持续集合，单目录 snapshot 不覆盖也不隐式建立持续订阅。取消或超时不安装新的持续集合。矿工与 `/account` binding 的通知放行规则不变。

单 key 判断：

~~~
本地不存在：
    incoming Seq == 1 -> 接受

本地存在：
    incoming Seq > local Seq -> 新版本候选
    incoming Seq < local Seq -> 旧消息，忽略
    Seq 相同 + hash 相同 -> 幂等
    Seq 相同 + hash 不同 -> 冲突/触发当前集合校准

delete：
    必须精确 target 当前 RecordHash
~~~

如果本地已经没有 key，而有效 CoreNode 发来合法 Seq=1，直接接受。

这种设计有意不保存删除历史。极端情况下落后节点可能短暂传播旧生命周期的 Seq=1；最终由 current-set sync/anti-entropy 收敛，而不是通过 tombstone/history 增加长期状态。

### 7.5 Data / inventory

内容响应只能证明“这个 record 曾被请求并且签名有效”，不能证明它在响应到达时仍是当前状态。

P2P Store 必须提供当前记录接收、network baseline、带基线的目录 snapshot 安装和目录枚举能力，不再保留可选 CurrentStateStore 或旧 generic mirror 安装分支。无当前上下文的旧 Data 不直接恢复已删除值；需要时触发当前集合校准。

---

## 8. P2P 当前集合同步

节点间同步只同步**当前网络可见 records**：

~~~
Path/Scope
captured snapshot root
snapshot 创建时的固定校验高度
active records
~~~

不包含：

~~~
generation
tombstone
delete floor
mutation history
source ownership
FREE_LOCAL
~~~

完整 snapshot 安装前必须验证：

- source/session；
- record 签名；
- namespace/permission；
- fee；
- scope；
- active set root；
- count/size/expiry 等当前状态 metadata；
- 下载期间本地状态未发生不兼容变化。

安装为原子替换。

如果当前集合中没有某 key，该 key 从 replica 中物理删除。

不同有效 CoreNode 可以成为新的 sync source；没有永久 first-source 绑定。

---

## 9. DID 与 /name、/svc

### 9.1 当前 owner 每次写都要验证

/name、/svc 的每次业务写入都通过 resolver 获取**当前 DID owner**。

因此：

- DID 转移后旧 owner 不能继续更新已有 key；
- 不因为历史 record 使用相同 pubkey 而跳过当前 owner 校验；
- 新 owner 可接管已有 records。

### 9.2 DID 转移后的重签

Receiving Wallet 执行显式 ResignDIDRecords：

1. 从服务节点读取完整当前 /svc/<did> collection；
2. 可选读取 /name/<did>；
3. 验证当前 records；
4. 保留 value 和 storage mode；
5. 有 TTL 的 record 保留原绝对 expiry，不因接管延长租期；
6. 新 owner 使用 Seq = old.Seq + 1 重签全部 records；
7. 一个 batch-CAS 原子提交。

如果全部 records 无法放入单个 batch，则直接拒绝，不拆成部分接管。

---

## 10. 存储模式

### 10.1 FREE_LOCAL

- TTL > 0；
- 只保存在当前 endpoint；
- 进入该 endpoint 的 Wallet current-set/generation；
- 不进入 P2P；
- 不进入 network snapshot；
- 到期物理删除；
- 受 signer/node quota；
- endpoint 切换不能把旧 FREE_LOCAL 当作新 endpoint 已确认数据。

### 10.2 AUTOPAY / PAID

- 通常 TTL = 0；
- 由 FeeVerifier / contract state 判断当前可用性；
- 网络可见记录参与 P2P current state；
- 停付后的 grace 是本地 retention 行为，不应制造删除历史。

network snapshot 缺项只替换当前网络视图，保留本地未到期的 AUTOPAY grace 数据；保留项不参与 network root 或 relay。incoming 同 key 的合法当前记录仍可替换该缓存，grace 到期由原有 retention prune 删除。安装过程中对本地可见性的判断只捕获一次，不因并发 retention refresh 在计数与清理之间改变决定。

### 10.3 AccountBound mailbox

/mail/<account>/... authoritative placement 在该账户当前绑定 CoreNode。

它使用 endpoint-local Wallet generation/snapshot，同样没有 delete floor。

message entry 通过 MessageManager internal append/delete 管理，不允许普通 DKVS PUT 绕过消息协议。

---

## 11. API 边界

### Wallet/Application API

~~~
GET  /v3/dkvs/config
GET  /v3/dkvs/record
GET  /v3/dkvs/key-state

POST /v3/dkvs/records/batch-cas

POST /v3/dkvs/active/sync
POST /v3/dkvs/active/watch

POST /v3/dkvs/prefixes/read
~~~

不存在：

~~~
/v3/dkvs/write-context
Wallet mutation log API
delete-history API
global generation API
~~~

### Node-local management

以下只用于 loopback/node administration：

~~~
GET  /v3/dkvs/checkpoint
GET  /v3/dkvs/snapshot
POST/DELETE/GET /v3/dkvs/subscriptions
~~~

PWA 业务代码不直接操作这些接口。

---

## 12. 并发、失败与恢复

### Wallet 多设备

~~~
同步最新 confirmed state
→ builder 生成下一业务 value
→ Seq/CAS/generation/request signature
→ 提交
~~~

失败：

- STALE_GENERATION：同步后重新 build；
- WRITE_CONFLICT：同步 key/prefix 后重新 build；
- INVALID_SEQUENCE：同步后重新 build；
- 网络未知：重放原始同一请求，不重新授权。

DKVS 不自动 merge 两个业务意图。

### Node offline/reconnect

节点离线期间：

- 不接收实时 Notify；
- 本地状态可能陈旧；
- 重连后先 current-set sync；
- sync 完成后才处理新 Notify。

---

## 13. 明确不采用的设计

为保持系统简单，当前版本明确不采用：

~~~
persistent tombstone
delete floor
mutation log
global generation
cross-source generation compare
per-prefix permanent source
multi-source version table
consensus/quorum
automatic CRDT merge
Wallet /write-context round trip
ACK writes confirmed replica
第二份 CoreNode binding acceptance registry
~~~

当未来需求确实需要增加上述复杂机制时，必须先给出不可由当前模型解决的真实失败场景，再单独评审。

---

## 14. 验收不变量

上线前长期保持：

1. P2P Notify payload 与签名 DKVS operation 完全一致，不含 generation。
2. P2P prefix/current-set sync 不传播 generation。
3. generation 只在同一 Wallet endpoint 范围比较。
4. 节点同步完成前只缓存 Notify，不应用；overflow 重新同步。
5. sync session 固定 source；session 结束后不形成永久 source ownership。
6. 来自其他有效 CoreNode 的实时 KV 不因 first-source 不同而拒绝。
7. 本地不存在 key 时，合法 Seq=1 realtime record 可接受。
8. 删除物理移除记录及索引，无 tombstone/delete floor。
9. 删除后合法重建从 Seq=1 开始。
10. Wallet PUT 不调用 /write-context，直接使用本地已同步 generation。
11. Wallet request signature 覆盖 generation + CAS + mutation + endpoint + RequestID。
12. ACK 不改变 confirmed replica。
13. 当前 /account mapping 是 CoreNode binding 的唯一事实来源。
14. DID 每次写都验证当前 owner；转移后可整组原子重签。
15. FREE_LOCAL 不进入 P2P。
16. 完整同步只描述当前集合，不保存删除历史。
