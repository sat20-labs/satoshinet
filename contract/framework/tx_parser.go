package framework

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	l2common "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type ContractScriptResolver func(pkScript []byte) (contract.ContractAddress, bool, error)

func ContractScriptResolverForType(prefix string, contractType byte) ContractScriptResolver {
	return func(pkScript []byte) (contract.ContractAddress, bool, error) {
		addr, ok, err := contract.ParseContractPkScript(pkScript, prefix)
		if err != nil || !ok {
			return addr, ok, err
		}
		if contractType != 0 && addr.ContractType() != contractType {
			return contract.ContractAddress{}, false, nil
		}
		return addr, true, nil
	}
}

type ContractOutput struct {
	OutPoint OutPoint
	Vout     uint32
	Contract contract.ContractAddress
	TxOutput *l2common.TxOutput
}

func (o ContractOutput) AssetAmount(assetName string) (*scommon.Decimal, error) {
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	if assetName == contract.SatoshiAssetName {
		return scommon.NewDefaultDecimal(o.PlainValue()), nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	output := o.IndexerTxOutput()
	if output == nil {
		return ZeroDecimal(), nil
	}
	amount := output.GetAsset(name)
	if amount == nil {
		return ZeroDecimal(), nil
	}
	return amount, nil
}

func (o ContractOutput) PlainValue() int64 {
	output := o.IndexerTxOutput()
	if output == nil {
		return 0
	}
	return output.GetPlainSat()
}

func (o ContractOutput) IndexerTxOutput() *l2common.TxOutput {
	if o.TxOutput != nil {
		return o.TxOutput.Clone()
	}
	return nil
}

func (o ContractOutput) PhysicalValue() int64 {
	output := o.IndexerTxOutput()
	if output == nil {
		return 0
	}
	return output.OutValue.Value
}

func (o ContractOutput) TxAssets() wire.TxAssets {
	output := o.IndexerTxOutput()
	if output == nil {
		return nil
	}
	return output.OutValue.Assets.Clone()
}

func (o ContractOutput) PkScriptBytes() []byte {
	output := o.IndexerTxOutput()
	if output == nil {
		return nil
	}
	return cloneBytes(output.OutValue.PkScript)
}

func (o *ContractOutput) SubAssetAmount(assetName string, amount *scommon.Decimal) error {
	if o == nil || amount == nil || amount.Sign() == 0 {
		return nil
	}
	if amount.Sign() < 0 {
		return fmt.Errorf("asset amount must be non-negative")
	}
	output := o.IndexerTxOutput()
	if output == nil {
		return fmt.Errorf("missing contract output")
	}
	asset := &wire.AssetInfo{Amount: *amount.Clone()}
	if assetName == contract.SatoshiAssetName {
		asset.Name = l2common.ASSET_PLAIN_SAT
	} else {
		name := wire.NewAssetNameFromString(assetName)
		if name == nil {
			return ErrInvalidAsset
		}
		existing, err := output.OutValue.Assets.Find(name)
		if err != nil || existing == nil {
			return ErrInvalidAsset
		}
		asset.Name = *name
		asset.BindingSat = existing.BindingSat
	}
	if err := output.SubAsset(asset); err != nil {
		return err
	}
	output.OutPointStr = o.OutPoint.String()
	o.applyIndexerTxOutput(output)
	return nil
}

func (o *ContractOutput) applyIndexerTxOutput(output *l2common.TxOutput) {
	if o == nil || output == nil {
		return
	}
	o.TxOutput = output.Clone()
}

func indexerTxOutputFromWire(outpoint OutPoint, txOut *wire.TxOut) *l2common.TxOutput {
	if txOut == nil {
		return nil
	}
	return &l2common.TxOutput{
		UtxoId:      scommon.INVALID_ID,
		OutPointStr: outpoint.String(),
		OutValue:    *cloneWireTxOut(txOut),
	}
}

func cloneWireTxOut(txOut *wire.TxOut) *wire.TxOut {
	if txOut == nil {
		return nil
	}
	return &wire.TxOut{
		Value:    txOut.Value,
		Assets:   txOut.Assets.Clone(),
		PkScript: cloneBytes(txOut.PkScript),
	}
}

type OutPoint struct {
	TxID string
	Vout uint32
}

func (o OutPoint) String() string {
	return fmt.Sprintf("%s:%d", o.TxID, o.Vout)
}

type DeployPayload struct {
	Type            byte
	SubType         string
	Version         uint32
	GasLimit        int64
	DeployNonce     uint64
	ContractContent []byte
}

type InvokePayload struct {
	GasLimit  int64
	CallNonce uint64
	Action    string
	Param     []byte
}

type ParseSpec struct {
	ModuleName           string
	ContractType         byte
	DecodeDeploy         func([]byte) (DeployPayload, error)
	DecodeInvoke         func([]byte) (InvokePayload, error)
	AcceptResult         bool
	RequireResultLastOut bool
	ContractMatches      func(contract.ContractAddress) bool
}

type PayloadParseSpec struct {
	ModuleName           string
	ContractType         byte
	DecodeDeploy         func([]byte) (DeployPayload, error)
	DecodeInvoke         func([]byte) (InvokePayload, error)
	AcceptResult         bool
	RequireResultLastOut bool
	ContractMatches      func(contract.ContractAddress) bool
}

func ParseSpecFromPayloads(cfg PayloadParseSpec) ParseSpec {
	matches := cfg.ContractMatches
	if matches == nil && cfg.ContractType != 0 {
		matches = func(addr contract.ContractAddress) bool {
			return addr.ContractType() == cfg.ContractType
		}
	}
	return ParseSpec{
		ModuleName:           cfg.ModuleName,
		ContractType:         cfg.ContractType,
		DecodeDeploy:         cfg.DecodeDeploy,
		DecodeInvoke:         cfg.DecodeInvoke,
		AcceptResult:         cfg.AcceptResult,
		RequireResultLastOut: cfg.RequireResultLastOut,
		ContractMatches:      matches,
	}
}

func ParseTxFunc(spec func() ParseSpec) func(*wire.MsgTx, ContractScriptResolver) (ParsedTx, error) {
	return func(tx *wire.MsgTx, resolver ContractScriptResolver) (ParsedTx, error) {
		return ParseTx(tx, resolver, spec())
	}
}

func FindInvokeContractOutputsFunc(spec func() ParseSpec) func(*wire.MsgTx, ContractScriptResolver) ([]ContractOutput, error) {
	return func(tx *wire.MsgTx, resolver ContractScriptResolver) ([]ContractOutput, error) {
		return FindInvokeContractOutputs(tx, resolver, spec())
	}
}

func FindContractOutputsForContractFunc() func(*wire.MsgTx, ContractScriptResolver, contract.ContractAddress) ([]ContractOutput, error) {
	return func(tx *wire.MsgTx, resolver ContractScriptResolver, contractAddr contract.ContractAddress) ([]ContractOutput, error) {
		return FindContractOutputsForContract(tx, resolver, contractAddr)
	}
}

type ParsedTx struct {
	Type            contract.TxType
	Payload         []byte
	PayloadIndex    int
	Deploy          *DeployPayload
	Invoke          *InvokePayload
	Result          *contract.ResultPayload
	StateRoot       *contract.StateRootPayload
	ContractOutputs []ContractOutput
	Inputs          []OutPoint
}

func ParseTx(tx *wire.MsgTx, resolver ContractScriptResolver, spec ParseSpec) (ParsedTx, error) {

	if tx == nil {
		return ParsedTx{}, errors.New("missing transaction")
	}
	parsed := ParsedTx{PayloadIndex: -1}
	for i, txIn := range tx.TxIn {
		if txIn == nil {
			return ParsedTx{}, fmt.Errorf("nil input %d", i)
		}
		parsed.Inputs = append(parsed.Inputs, WireOutPointToFramework(txIn.PreviousOutPoint))
	}

	payloadParts := make([][]byte, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return ParsedTx{}, fmt.Errorf("nil output %d", i)
		}
		txType, payload, err := contract.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if parsed.Type == 0 {
			parsed.Type = txType
			parsed.PayloadIndex = i
		} else if parsed.Type != txType {
			return ParsedTx{},
				fmt.Errorf("transaction contains mixed %s OP_RETURN output types", spec.ModuleName)
		} else if txType != contract.TxTypeDeploy && txType != contract.TxTypeInvoke {
			return ParsedTx{},
				fmt.Errorf("transaction contains multiple singleton %s OP_RETURN outputs", spec.ModuleName)
		}
		payloadParts = append(payloadParts, payload)
	}
	if parsed.Type == 0 {
		return parsed, nil
	}
	for _, part := range payloadParts {
		parsed.Payload = append(parsed.Payload, part...)
	}

	switch parsed.Type {
	case contract.TxTypeDeploy:
		payload, err := spec.DecodeDeploy(parsed.Payload)
		if err != nil {
			return ParsedTx{}, err
		}
		parsed.Deploy = &payload
	case contract.TxTypeInvoke:
		payload, err := spec.DecodeInvoke(parsed.Payload)
		if err != nil {
			return ParsedTx{}, err
		}
		parsed.Invoke = &payload
		outputs, err := FindInvokeContractOutputs(tx, resolver, spec)
		if err != nil {
			return ParsedTx{}, err
		}
		parsed.ContractOutputs = outputs
	case contract.TxTypeResult:
		if !spec.AcceptResult {
			return ParsedTx{},
				fmt.Errorf("%s RESULT transactions are built by block execution and are not accepted as external input", spec.ModuleName)
		}
		if len(payloadParts) != 1 {
			return ParsedTx{}, fmt.Errorf("%s RESULT must use exactly one OP_RETURN", spec.ModuleName)
		}
		if spec.RequireResultLastOut && parsed.PayloadIndex != len(tx.TxOut)-1 {
			return ParsedTx{}, fmt.Errorf("%s RESULT OP_RETURN must be the last output", spec.ModuleName)
		}
		payload, err := contract.DecodeResultPayload(parsed.Payload)
		if err != nil {
			return ParsedTx{}, err
		}
		parsed.Result = &payload
	case contract.TxTypeCoinbaseStateRoot:
		if len(payloadParts) != 1 {
			return ParsedTx{}, fmt.Errorf("%s state root must use exactly one OP_RETURN", spec.ModuleName)
		}
		payload, err := contract.DecodeStateRootPayload(parsed.Payload)
		if err != nil {
			return ParsedTx{}, err
		}
		parsed.StateRoot = &payload
	default:
		return ParsedTx{}, fmt.Errorf("unsupported %s tx type %d", spec.ModuleName, parsed.Type)
	}
	return parsed, nil
}

func FindInvokeContractOutputs(tx *wire.MsgTx, resolver ContractScriptResolver,
	spec ParseSpec) ([]ContractOutput, error) {

	if resolver == nil {
		return nil, errors.New("missing contract script resolver")
	}
	txid := tx.TxID()
	var contractAddr *contract.ContractAddress
	outputs := make([]ContractOutput, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return nil, fmt.Errorf("nil output %d", i)
		}
		addr, ok, err := resolver(txOut.PkScript)
		if err != nil {
			return nil, err
		}
		if !ok || (spec.ContractMatches != nil && !spec.ContractMatches(addr)) {
			continue
		}
		if contractAddr == nil {
			cp := addr
			contractAddr = &cp
		} else if !contractAddr.Equal(addr) {
			return nil, fmt.Errorf("%s INVOKE outputs target multiple contract addresses", spec.ModuleName)
		}
		vout := uint32(i)
		outpoint := OutPoint{TxID: txid, Vout: vout}
		outputs = append(outputs, ContractOutput{
			OutPoint: outpoint,
			Vout:     vout,
			Contract: addr,
			TxOutput: indexerTxOutputFromWire(outpoint, txOut),
		})
	}
	if len(outputs) == 0 {
		return nil, fmt.Errorf("%s INVOKE has no contract output", spec.ModuleName)
	}
	return outputs, nil
}

func FindContractOutputsForContract(tx *wire.MsgTx, resolver ContractScriptResolver,
	contractAddr contract.ContractAddress) ([]ContractOutput, error) {

	if resolver == nil {
		return nil, errors.New("missing contract script resolver")
	}
	txid := tx.TxID()
	outputs := make([]ContractOutput, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return nil, fmt.Errorf("nil output %d", i)
		}
		addr, ok, err := resolver(txOut.PkScript)
		if err != nil {
			return nil, err
		}
		if !ok || !addr.Equal(contractAddr) {
			continue
		}
		vout := uint32(i)
		outpoint := OutPoint{TxID: txid, Vout: vout}
		outputs = append(outputs, ContractOutput{
			OutPoint: outpoint,
			Vout:     vout,
			Contract: addr,
			TxOutput: indexerTxOutputFromWire(outpoint, txOut),
		})
	}
	return outputs, nil
}

func HasContractOutput(tx *wire.MsgTx, resolver ContractScriptResolver) (bool, error) {
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		_, ok, err := resolver(txOut.PkScript)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func WireOutPointToFramework(out wire.OutPoint) OutPoint {
	return OutPoint{
		TxID: out.Hash.String(),
		Vout: out.Index,
	}
}

func CloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func cloneBytes(src []byte) []byte {
	return CloneBytes(src)
}
