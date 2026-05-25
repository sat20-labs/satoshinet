package agent

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type AssetAmount struct {
	AssetName string
	Amount    *scommon.Decimal
}

type TxFunding struct {
	Value  int64
	Assets []AssetAmount
}

type DeployTxBuildRequest struct {
	ContractPrefix  string
	Subtype         string
	AgentVersion    uint32
	Deployer        string
	Random          []byte
	ContractContent []byte
	GasLimit        uint64
	Funding         TxFunding
	Inputs          []wire.OutPoint
	ChangeOutputs   []*wire.TxOut
}

type InvokeTxBuildRequest struct {
	Contract      ContractAddress
	GasLimit      uint64
	CallNonce     uint64
	Action        string
	Param         []byte
	Funding       TxFunding
	Inputs        []wire.OutPoint
	ChangeOutputs []*wire.TxOut
}

func BuildDeployTx(req DeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	version := req.AgentVersion
	if version == 0 {
		version = CurrentAgentVersion
	}
	contract, _, err := DeriveContractAddress(prefix, req.Subtype, req.ContractContent, req.Deployer, req.Random)
	if err != nil {
		return nil, ContractAddress{}, err
	}
	if err := req.Funding.Validate(false); err != nil {
		return nil, ContractAddress{}, err
	}
	scripts, err := DeployNullDataScripts(DeployPayload{
		GasLimit:        req.GasLimit,
		Subtype:         req.Subtype,
		AgentVersion:    version,
		Deployer:        req.Deployer,
		Random:          cloneBytes(req.Random),
		ContractContent: cloneBytes(req.ContractContent),
	})
	if err != nil {
		return nil, ContractAddress{}, err
	}
	contractOut, err := req.Funding.ContractTxOut(contract)
	if err != nil {
		return nil, ContractAddress{}, err
	}

	tx := newAgentUnsignedTx(req.Inputs)
	for _, script := range scripts {
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}
	tx.AddTxOut(contractOut)
	addTxOutCopies(tx, req.ChangeOutputs)
	return tx, contract, nil
}

func BuildInvokeTx(req InvokeTxBuildRequest) (*wire.MsgTx, error) {
	if err := req.Funding.Validate(false); err != nil {
		return nil, err
	}
	scripts, err := InvokeNullDataScripts(InvokePayload{
		GasLimit:  req.GasLimit,
		CallNonce: req.CallNonce,
		Action:    req.Action,
		Param:     cloneBytes(req.Param),
	})
	if err != nil {
		return nil, err
	}
	contractOut, err := req.Funding.ContractTxOut(req.Contract)
	if err != nil {
		return nil, err
	}

	tx := newAgentUnsignedTx(req.Inputs)
	for _, script := range scripts {
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}
	tx.AddTxOut(contractOut)
	addTxOutCopies(tx, req.ChangeOutputs)
	return tx, nil
}

func (f TxFunding) Validate(requireFunding bool) error {
	if f.Value < 0 {
		return fmt.Errorf("funding value must not be negative")
	}
	if requireFunding && f.Value == 0 && len(f.Assets) == 0 {
		return fmt.Errorf("funding must contain satoshi or asset amount")
	}
	for _, asset := range f.Assets {
		if asset.AssetName == "" || asset.AssetName == SatoshiAssetName {
			return fmt.Errorf("invalid funding asset name %q", asset.AssetName)
		}
		if asset.Amount == nil || asset.Amount.IsZero() {
			return fmt.Errorf("funding asset %s amount is zero", asset.AssetName)
		}
		if wire.NewAssetNameFromString(asset.AssetName) == nil {
			return fmt.Errorf("invalid funding asset name %q", asset.AssetName)
		}
		if asset.Amount.Sign() < 0 {
			return fmt.Errorf("funding asset %s amount is negative", asset.AssetName)
		}
	}
	return nil
}

func (f TxFunding) ContractTxOut(contract ContractAddress) (*wire.TxOut, error) {
	assets, err := f.WireAssets()
	if err != nil {
		return nil, err
	}
	return NewContractTxOut(f.Value, assets, contract)
}

func (f TxFunding) WireAssets() (wire.TxAssets, error) {
	assets := make(wire.TxAssets, 0, len(f.Assets))
	for _, asset := range f.Assets {
		name := wire.NewAssetNameFromString(asset.AssetName)
		if name == nil {
			return nil, fmt.Errorf("invalid funding asset name %q", asset.AssetName)
		}
		if asset.Amount == nil {
			return nil, fmt.Errorf("funding asset %s amount is nil", asset.AssetName)
		}
		assets = append(assets, wire.AssetInfo{
			Name:   *name,
			Amount: *asset.Amount.Clone(),
		})
	}
	sort.Slice(assets, func(i, j int) bool {
		left := assets[i].Name
		right := assets[j].Name
		if left.Protocol != right.Protocol {
			return left.Protocol < right.Protocol
		}
		if left.Type != right.Type {
			return left.Type < right.Type
		}
		return left.Ticker < right.Ticker
	})
	return assets, nil
}

func NewContractTxOut(value int64, assets wire.TxAssets, contract ContractAddress) (*wire.TxOut, error) {
	script, err := ContractPkScript(contract)
	if err != nil {
		return nil, err
	}
	return wire.NewTxOut(value, assets.Clone(), script), nil
}

func MsgTxHex(tx *wire.MsgTx) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("missing transaction")
	}
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf.Bytes()), nil
}

func ParseOutPoint(s string) (wire.OutPoint, error) {
	outpoint, err := wire.NewOutPointFromString(s)
	if err == nil {
		return *outpoint, nil
	}
	hash, hashErr := chainhash.NewHashFromStr(s)
	if hashErr == nil {
		return wire.OutPoint{Hash: *hash, Index: 0}, nil
	}
	return wire.OutPoint{}, err
}

func newAgentUnsignedTx(inputs []wire.OutPoint) *wire.MsgTx {
	tx := wire.NewMsgTx(2)
	for i := range inputs {
		tx.AddTxIn(wire.NewTxIn(&inputs[i], nil, nil))
	}
	return tx
}

func addTxOutCopies(tx *wire.MsgTx, outputs []*wire.TxOut) {
	for _, output := range outputs {
		if output == nil {
			continue
		}
		cp := *output
		cp.PkScript = cloneBytes(output.PkScript)
		cp.Assets = output.Assets.Clone()
		tx.AddTxOut(&cp)
	}
}
