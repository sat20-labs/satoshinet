package blockchain

import (
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

// Asset envelopes are consensus data. These checks apply to every output,
// including anchor, coinbase and contract Result outputs, without a height gate.
// Wire encoding deliberately remains independent of transaction semantics.
func checkAssetOutput(output *wire.TxOut) error {
	if output == nil {
		return ruleError(ErrBadTxOutValue, "nil transaction output")
	}
	for _, asset := range output.Assets {
		if asset.Name == (wire.AssetName{}) {
			return ruleError(ErrBadTxOutValue, "satoshi (::) must use value, not TxAssets")
		}
	}
	carrier, err := contract.RequiredBindingSats(output.Assets)
	if err != nil {
		return ruleError(ErrBadTxOutValue, err.Error())
	}
	if output.Value < carrier {
		return ruleError(ErrBadTxOutValue, fmt.Sprintf("insufficient carrier sats for bound assets: %d < %d", output.Value, carrier))
	}
	return nil
}

// Collect metadata before quantities are aggregated, so conflicting inputs
// cannot be silently merged into whichever BindingSat happened to appear first.
func checkTransactionAssetBindings(tx *wire.MsgTx, view *UtxoViewpoint) error {
	metadata := make(map[wire.AssetName]uint32)
	for _, input := range tx.TxIn {
		entry := view.LookupEntry(input.PreviousOutPoint)
		if entry == nil || entry.IsSpent() {
			return ruleError(ErrMissingTxOut, fmt.Sprintf("missing asset metadata input %s", input.PreviousOutPoint))
		}
		for _, asset := range entry.TxAssets() {
			if asset.Name == (wire.AssetName{}) {
				return ruleError(ErrBadTxOutValue, "satoshi (::) must not appear in input TxAssets")
			}
			if err := asset.Amount.Validate(); err != nil || asset.Amount.Sign() < 0 {
				return ruleError(ErrBadTxOutValue, fmt.Sprintf("invalid input asset %s amount", asset.Name.String()))
			}
			if binding, ok := metadata[asset.Name]; ok && binding != asset.BindingSat {
				return ruleError(ErrBadTxOutValue, fmt.Sprintf("conflicting input BindingSat for asset %s", asset.Name.String()))
			}
			metadata[asset.Name] = asset.BindingSat
		}
	}
	for _, output := range tx.TxOut {
		for _, asset := range output.Assets {
			binding, ok := metadata[asset.Name]
			if !ok || binding != asset.BindingSat {
				return ruleError(ErrBadTxOutValue, fmt.Sprintf("asset %s BindingSat differs from its inputs", asset.Name.String()))
			}
		}
	}
	return nil
}

// Coinbase has no ordinary inputs. Its asset metadata is inherited from the
// validated transaction fees, not chosen by the block producer.
func checkCoinbaseAssetBindings(actual, expected wire.TxAssets) error {
	for _, asset := range actual {
		original, err := expected.Find(&asset.Name)
		if err != nil || original == nil || original.BindingSat != asset.BindingSat {
			return ruleError(ErrBadCoinbaseValue, fmt.Sprintf("coinbase asset %s BindingSat differs from fee assets", asset.Name.String()))
		}
	}
	return nil
}
