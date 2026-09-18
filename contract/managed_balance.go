package contract

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

// ManagedBalance records accepted quantities, not ownership of individual
// UTXOs. Value includes asset-bearing sats; Assets retains the existing asset
// precision and binding metadata. Physical balances remain a UTXO-view fact.
type ManagedBalance struct {
	Value  int64         `json:"value"`
	Assets wire.TxAssets `json:"assets,omitempty"`
}

func (b ManagedBalance) Clone() ManagedBalance {
	return ManagedBalance{Value: b.Value, Assets: b.Assets.Clone()}
}

func (b ManagedBalance) Validate() error {
	if b.Value < 0 {
		return fmt.Errorf("negative managed satoshi amount")
	}
	seen := make(map[string]struct{}, len(b.Assets))
	for _, asset := range b.Assets {
		name := asset.Name.String()
		if name == SatoshiAssetName || asset.Name.Protocol == "" {
			return fmt.Errorf("invalid managed asset %q", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate managed asset %s", name)
		}
		seen[name] = struct{}{}
		if err := asset.Amount.Validate(); err != nil {
			return fmt.Errorf("managed asset %s: %w", name, err)
		}
		if asset.Amount.Sign() < 0 {
			return fmt.Errorf("negative managed asset %s", name)
		}
	}
	return nil
}

// Credit and Debit stage their changes so an error never partially updates the
// ledger. The caller commits the containing runtime with the validated block.
func (b *ManagedBalance) Credit(value int64, assets wire.TxAssets) error {
	if b == nil {
		return fmt.Errorf("missing managed balance")
	}
	incoming := ManagedBalance{Value: value, Assets: assets.Clone()}
	if err := b.Validate(); err != nil {
		return err
	}
	if err := incoming.Validate(); err != nil {
		return err
	}
	if err := b.checkAssetBindings(assets); err != nil {
		return err
	}
	if value > math.MaxInt64-b.Value {
		return fmt.Errorf("managed satoshi amount overflows int64")
	}
	next := b.Clone()
	next.Value += value
	if err := next.Assets.Merge(assets.Clone()); err != nil {
		return err
	}
	next.normalize()
	*b = next
	return nil
}

func (b *ManagedBalance) Debit(value int64, assets wire.TxAssets) error {
	if b == nil {
		return fmt.Errorf("missing managed balance")
	}
	spend := ManagedBalance{Value: value, Assets: assets.Clone()}
	if err := b.Validate(); err != nil {
		return err
	}
	if err := spend.Validate(); err != nil {
		return err
	}
	if err := b.checkAssetBindings(assets); err != nil {
		return err
	}
	if value > b.Value {
		return fmt.Errorf("managed sats deficit: spend %d, available %d", value, b.Value)
	}
	next := b.Clone()
	next.normalize()
	next.Value -= value
	if len(assets) != 0 {
		if err := next.Assets.Split(assets.Clone()); err != nil {
			return fmt.Errorf("managed asset deficit: %w", err)
		}
	}
	next.normalize()
	*b = next
	return nil
}

func (b ManagedBalance) checkAssetBindings(assets wire.TxAssets) error {
	for _, incoming := range assets {
		for _, existing := range b.Assets {
			if incoming.Name == existing.Name && incoming.BindingSat != existing.BindingSat {
				return fmt.Errorf("conflicting binding metadata for managed asset %s", incoming.Name.String())
			}
		}
	}
	return nil
}

func (b ManagedBalance) IsZero() bool {
	if b.Value != 0 {
		return false
	}
	for _, asset := range b.Assets {
		if asset.Amount.Sign() != 0 {
			return false
		}
	}
	return true
}

// PlainValue follows SatoshiNet L2 partial-binding semantics: only complete
// BindingSat groups reserve sats. Partial asset remainders are allowed to stay
// unbound until they are recombined or leave L2.
func (b ManagedBalance) PlainValue() (int64, error) {
	if err := b.Validate(); err != nil {
		return 0, err
	}
	reserved := new(big.Int)
	for _, asset := range b.Assets {
		if asset.BindingSat == 0 || asset.Amount.Sign() == 0 {
			continue
		}
		amount := asset.Amount.NewPrecision(0).Value
		units := new(big.Int).Quo(amount, new(big.Int).SetUint64(uint64(asset.BindingSat)))
		reserved.Add(reserved, units)
	}
	if reserved.Cmp(big.NewInt(b.Value)) >= 0 {
		return 0, nil
	}
	return b.Value - reserved.Int64(), nil
}

func (b ManagedBalance) AssetAmount(name string) (*scommon.Decimal, error) {
	if name == "" {
		return nil, fmt.Errorf("empty managed asset name")
	}
	if name == SatoshiAssetName {
		plain, err := b.PlainValue()
		if err != nil {
			return nil, err
		}
		return scommon.NewDefaultDecimal(plain), nil
	}
	for _, asset := range b.Assets {
		if asset.Name.String() == name {
			return asset.Amount.Clone(), nil
		}
	}
	return scommon.NewDefaultDecimal(0), nil
}

func (b *ManagedBalance) normalize() {
	assets := make(wire.TxAssets, 0, len(b.Assets))
	for _, asset := range b.Assets {
		if asset.Amount.Sign() != 0 {
			assets = append(assets, asset)
		}
	}
	// TxAssets.Find/Split require the same tuple ordering as TxAssetsBuilder.
	sort.Slice(assets, func(i, j int) bool {
		a, c := assets[i].Name, assets[j].Name
		if a.Protocol != c.Protocol {
			return a.Protocol < c.Protocol
		}
		if a.Type != c.Type {
			return a.Type < c.Type
		}
		return a.Ticker < c.Ticker
	})
	b.Assets = assets
}

func (b ManagedBalance) MarshalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	next := b.Clone()
	next.normalize()
	type plain ManagedBalance
	return json.Marshal(plain(next))
}

func (b *ManagedBalance) UnmarshalJSON(data []byte) error {
	type plain ManagedBalance
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	next := ManagedBalance(decoded)
	if err := next.Validate(); err != nil {
		return err
	}
	next.normalize()
	*b = next
	return nil
}
