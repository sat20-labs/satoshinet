# RGB11 registration and SatoshiNet asset names

The public contract registry retains the complete RGB11 ContractID. Public
Transcend uses `rgb11:f:<normalized_ticker>@<contract_id_suffix>` in its signed
deployment content, contract path, and SatoshiNet deposit Anchor. The suffix is
the last four characters of the canonical 64-character lowercase hexadecimal
ContractID. Leading zeros are retained. The Bitcoin RGB wallet continues to
key balances and proofs by the full ContractID.

## DKVS records

| Path | Value |
| --- | --- |
| `/contract/rgb11/<contract_id>` | Version byte `1` and a CompactSize-prefixed standard public RGB contract file. |
| `/contract/rgb11-name/<normalized_ticker>/<contract_id_suffix>` | Version byte `1` and the 32-byte full ContractID. |
| `/contract/evm/source/<contract_address>` | EVM source record managed by the EVM source verifier. |

The Core authority signs the RGB records. The storage envelope has `TTL=0`,
zero flags, no fee proof, and a compressed signer key. A public contract uses
`Seq=1`. A short name uses `Seq=1` to claim the key atomically. After reading
back the matching claim from the same registry, Core signs and publishes
`Seq=2` for the same key and value. Only `Seq=2` is valid in an Anchor. A retry
with the same value can complete a previously committed claim. Two full
ContractIDs producing the same short ticker conflict at the immutable key.
Generic wallet DKVS writes and deletes cannot alter either record.

## Registration and deployment

`POST /v3/rgb11/contracts/register` on the STP Core accepts the public RGB
contract under `contract_consignment_base64`. A request with `register_name`
set to `true` also registers the deterministic SatoshiNet name. Core verifies
the public Genesis, asset type, and Bitcoin network, then computes the name
from the Genesis ticker and full ID. The public contract path also serves
wallet recovery and can be populated independently.

The wallet resolves the name before constructing the deployment request. The
Core validates the registered name on the Transcend deployment entry points.
The Anchor's native proof carries the signed binding and RGB history. Nodes
validate the signature, independently derive the expected name from the proof,
and check that the proof's full ContractID matches the binding. An RGB-only
public Anchor credits zero sats on SatoshiNet.

The SatoshiNet `/v3/tick/info/:ticker` response exposes the on-chain name in
`name` and `displayname`. It includes `contract_id`
when the node can verify the signed DKVS name binding.

For `TRGB241`, the full ContractID
`8650c89a895329747b8140166be3296432e8c0205a2ac838e097d154c53f0e8a`
yields `rgb11:f:trgb241@0e8a`. The old testnet deployment used the full
hexadecimal ID in the ticker field and requires a new deployment under the
canonical name.
