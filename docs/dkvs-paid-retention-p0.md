# DKVS Paid Retention P0

状态：Review

## 目标

为账户恢复和 RGB11 提供可立即使用的 DKVS 保存规则，不引入套餐、按字节计费、第三方代付或多种付费合约。

## 两种保存模式

### FREE_LOCAL

- 未付费 record 由接入节点按 `GET /v3/dkvs/config` 返回的本地策略缓存。
- record 必须设置非零 TTL，且不能超过节点 `max_ttl_ms`。
- 只保存在该节点，不参与 DKVS P2P relay。
- TTL 到期后物理删除。
- 续期由 owner 使用更高 `Seq`、新的 `IssueTime` 和 TTL 重新签名提交。

### AUTOPAY

- 付费 record 使用 `FeeMode=AUTOPAY`。
- `TTL=0`、`ExpiryHeight=0`；record 自身不保存未来租期。
- 节点拒绝携带非零 `TTL` 或 `ExpiryHeight` 的 AUTOPAY record，避免同时存在两套有效期来源。
- `amount_per_block` 决定该 payer 可占用的 active record slot：

```text
max_records = floor(amount_per_block / full_record_fee_per_block)
```

- AUTOPAY 每个区块扣除一次 `amount_per_block`。
- DKVS 默认 AUTOPAY 合约的 recipient 为空，因此该区块费用作为 miner fee 支付给当前区块 miner。
- 不增加 `pay_interval_blocks`；支付周期固定为 1 个区块。
- 测试网最低 `amount_per_block` 和 `full_record_fee_per_block` 均为 `1`。

## 付费有效性

AUTOPAY delegate 每成功支付一个区块，更新：

```text
last_pay_height = current_block
paid_block_count += 1
```

付费 record 只有在以下条件满足时才可以写入和参与网络 relay：

```text
last_pay_height >= current_block
```

新 delegate 必须至少完成一次区块支付后才能写入 AUTOPAY record。

- 新 record 在 fee proof 验证通过后立即把该次当前区块付款登记到本节点 retention cache，因此首次写入可以立即 relay。
- 远端节点接收 record 时执行相同验证和登记，允许继续向后传播。
- write-time retention 登记只使用刚刚通过验证的链上状态，不放宽任何付款条件。
- 测试和调用方也必须提供 `CurrentBlock` 与对应的 `LastPayHeight`，不再接受仅凭 `active + balance` 的旧状态。

节点启动、重启或付费状态缓存尚未刷新时采用 fail-closed：

- record 仍可从本地读取；
- 在当前区块支付得到验证之前不参与 relay；
- 状态刷新通过后自动恢复 relay，不需要重写 record。

## 停止付费后的缓存

当 delegate 没有继续按区块支付：

1. record 从下一次合约状态刷新开始停止 relay；
2. 已保存 record 在各节点本地进入临时缓存状态；
3. 本地缓存期限使用该节点 `max_ttl_ms`；
4. 缓存期内可以从已有节点读取，但不能向新节点同步；
5. 缓存期结束后物理删除并释放 record slot；
6. 缓存期内恢复支付，可重新变为 relayable，无需重写 record。

本地缓存截止高度按聪网 12 秒出块折算：

```text
grace_blocks = ceil(max_ttl_ms / 12000)
local_expiry_height = last_pay_height + grace_blocks
```

## 内容更新和删除

- 内容更新：owner 使用 `Seq+1` 重新签名提交。
- 临时缓存升级为付费：相同 key 使用 `Seq+1`、`FeeMode=AUTOPAY`、`TTL=0`、`ExpiryHeight=0` 重新提交。
- AUTOPAY record 不执行 record 级续期；持续充值并逐块支付即保持有效。
- 删除：继续使用签名 tombstone，不收存储费用，并立即释放容量。

## 配置来源

`GET /v3/dkvs/config` 只返回当前连接节点的 FREE_LOCAL 缓存政策，例如：

```json
{
  "enabled": true,
  "max_ttl_ms": 2592000000,
  "max_records_per_signer": 100,
  "max_bytes_per_signer": 1048576
}
```

AUTOPAY 合约地址、费用资产、最低每区块费用和每条 record 费率来自当前网络的 DKVS AUTOPAY 配置及链上合约状态。

PWA 和 Wallet SDK 只展示 SDK 返回的临时缓存和付费保存结果，不自行定义 TTL 或支付规则。

## P0 不做

- 不增加支付周期参数；
- 不做未来期限预付；
- 不做 ONESHOT 或 LEASE；
- 不做按字节计费；
- 不做第三方 payer；
- 不做不同 namespace 的差异定价；
- 不兼容旧测试设计、旧合约内容和旧合约地址。
