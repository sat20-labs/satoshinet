package contract

import (
	"fmt"
	"math"
	"math/big"

	"github.com/sat20-labs/satoshinet/wire"
)

// RequiredBindingSats uses the existing L2 rule: only complete BindingSat
// groups need carrier sats. It never narrows an asset amount to int64 before
// division. A very large asset quantity can still require a small valid value.
func RequiredBindingSats(assets wire.TxAssets) (int64, error) {
	var total int64
	for _, asset := range assets {
		if err := asset.Amount.Validate(); err != nil {
			return 0, fmt.Errorf("invalid asset %s amount: %w", asset.Name.String(), err)
		}
		if asset.Amount.Sign() < 0 {
			return 0, fmt.Errorf("negative asset %s amount", asset.Name.String())
		}
		if asset.BindingSat == 0 { continue }
		amount, ok := new(big.Rat).SetString(asset.Amount.String())
		if !ok { return 0, fmt.Errorf("invalid bound asset %s amount", asset.Name.String()) }
		denominator := new(big.Int).Mul(amount.Denom(), new(big.Int).SetUint64(uint64(asset.BindingSat)))
		groups := new(big.Int).Quo(amount.Num(), denominator)
		if !groups.IsInt64() || groups.Sign() < 0 {
			return 0, fmt.Errorf("asset %s carrier sats overflow int64", asset.Name.String())
		}
		n := groups.Int64()
		if n > math.MaxInt64-total { return 0, fmt.Errorf("carrier sats overflow int64") }
		total += n
	}
	return total, nil
}
