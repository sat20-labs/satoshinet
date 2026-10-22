# STP native asset proofs

RGB11 fungible mappings use the existing Anchor transaction, Core signature,
`TxAssets` ledger and funding-outpoint replay protection. RGB balances are
derived from the RGB11 consignment validator. Indexer responses supply only
Bitcoin raw transactions, confirmation status, canonical block headers and
outspend facts through existing v3 endpoints.

## Activation and compatibility

`chaincfg.Params.NativeAssetHeight` is zero on every default network. A positive
height explicitly enables native admission, independently of POS v2. Mempool,
block admission and block indexing use the same height rule. Native invoices
always bind their outputs, even before POS v2 activation. An anchor claiming
RGB11 without a complete native proof is rejected. Existing anchors without
native material retain their previous validation path.

This activation controls new mappings. Ordinary transfers and descending
transactions of assets already mapped are not gated by it. Asset-only RGB
descending outputs may carry zero BTC; the BTC carrier remains on Bitcoin.

## Proof transport and signing order

`anchortx/nativeproof.BuildRGB11(consignment, reveals)` accepts a real RGB11
contract or transfer and canonical disclosed graph seals. It normalizes the
consignment to the standard binary container. The bounded STPN version 1
envelope contains that container and strictly ordered, unique seal reveals.

1. Construct the canonical unsigned anchor and all settlement outputs.
2. Call `indexer/common.SetNativeAssetProof(tx, proof)`. The helper installs one
   witness item and one zero-value, assetless native metadata output carrying
   SHA256 of the complete proof. It rejects an already signed invoice.
3. Obtain `indexer/common.AnchorInvoice(tx, bindOutputs)` and sign it. Native
   proof presence automatically forces output binding.
4. Insert the signature with `StandardAnchorScriptWithSig`.

The witness body, digest and output distribution cannot be substituted without
invalidating proof or invoice validation. Proofs remain in ordinary persisted
transactions; no parallel native ledger or alternate replay key is introduced.

## Validation authority

`nativeproof.Validate(proof, Source, resolver, mode)` returns canonical asset
identity, genesis precision, full source amount, source output and the RGB11
core history report. `Source.Assets` must claim exactly one `rgb11:f:<contractid>`
asset, use the complete lower-case 32-byte contract ID, and set `BindingSat=0`.
All token allocations at the source outpoint are summed. Partial claims,
coasset claims, NFTs, control rights, incorrect precision and mismatched
genesis networks fail.

| Mode | Evidence requirement | Authority |
| --- | --- | --- |
| `Current` | All witnesses mined; exact source exists and is unspent | New mapping precondition, still subject to signature and replay checks |
| `Historical` | Mined raw witnesses and exact historical source | Replay/material validation; does not query current source UTXO status |
| `Prepared` | Core-validated embedded witnesses may be unbroadcast | Material only; never authorizes a new mapping or a spendable balance |

Explicitly archived witnesses and contradictory mined evidence fail even in
prepared mode. Historical or prepared report fields never become wallet
ownership merely because their proof material validates.

`AnchorConfig.NativeAssetResolver` can supply an existing Bitcoin facts adapter.
When omitted, the node uses the configured indexer's existing rawtx/status,
block-header and outspend APIs. Block indexing uses its locked Core membership
view and retains the complete transaction witness for historical validation.

## Bridge trust boundary

Native proof validation establishes the Bitcoin allocation and its history; it
does not replace STP's Core co-signature authorization or its custody model.
Funding-outpoint replay protection rejects a repeated mapping of that outpoint.
It is not a global RGB liability ledger that recognizes every descendant of a
previously mapped allocation. Honest Core signers must enforce the corresponding
STP settlement and burn obligations before authorizing a new anchor. The wallet
and public-channel receipt checks carry those obligations through normal STP
flows. Colluding custodians are not made harmless by this proof format.

The configured Bitcoin evidence service remains a trust and availability
dependency. This change neither implements a Bitcoin light client inside
SatoshiNet nor enables native admission on any default network.
