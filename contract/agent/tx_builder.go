package agent

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type DeployTxBuildRequest struct {
	ContractPrefix  string
	Subtype         string
	AgentVersion    uint32
	Deployer        string
	Random          []byte
	ContractContent []byte
	GasLimit        uint64
	Funding         wire.TxOut
	Inputs          []wire.OutPoint
	ChangeOutputs   []*wire.TxOut
}

type InvokeTxBuildRequest struct {
	Contract      ContractAddress
	GasLimit      uint64
	CallNonce     uint64
	Action        string
	Param         []byte
	Funding       wire.TxOut
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
	if err := validateFundingTxOut(req.Funding, false); err != nil {
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
	contractOut, err := contractTxOutFromFunding(req.Funding, contract)
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
	if err := validateFundingTxOut(req.Funding, false); err != nil {
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
	contractOut, err := contractTxOutFromFunding(req.Funding, req.Contract)
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

func validateFundingTxOut(funding wire.TxOut, requireFunding bool) error {
	return contractcommon.ValidateFundingTxOut(funding, contractcommon.FundingValidation{
		RequireFunding: requireFunding,
	})
}

func contractTxOutFromFunding(funding wire.TxOut, contract ContractAddress) (*wire.TxOut, error) {
	return NewContractTxOut(funding.Value, funding.Assets, contract)
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
