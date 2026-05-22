package template

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type ParsedTx struct {
	Type            TxType
	Payload         []byte
	TemplateIndex   int
	Deploy          *DeployPayload
	Invoke          *InvokePayload
	StateRoot       *StateRootPayload
	ContractOutputs []ContractOutput
	Inputs          []OutPoint
}

type ContractOutput struct {
	OutPoint OutPoint
	Vout     uint32
	Contract ContractAddress
	Value    int64
	Assets   wire.TxAssets
	PkScript []byte
}

func (o ContractOutput) AssetAmount(assetName string) (*scommon.Decimal, error) {
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	if assetName == SatoshiAssetName {
		if o.Value < 0 {
			return nil, fmt.Errorf("contract output %s has negative value", o.OutPoint)
		}
		return scommon.NewDefaultDecimal(o.Value), nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	asset, err := o.Assets.Find(name)
	if err != nil || asset == nil {
		return scommon.NewDefaultDecimal(0), nil
	}
	return asset.Amount.Clone(), nil
}

func ParseTx(tx *wire.MsgTx, resolver ContractScriptResolver) (ParsedTx, error) {
	if tx == nil {
		return ParsedTx{}, errors.New("missing transaction")
	}
	parsed := ParsedTx{TemplateIndex: -1}
	for i, txIn := range tx.TxIn {
		if txIn == nil {
			return ParsedTx{}, fmt.Errorf("nil input %d", i)
		}
		parsed.Inputs = append(parsed.Inputs, WireOutPointToTemplate(txIn.PreviousOutPoint))
	}

	payloadParts := make([][]byte, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return ParsedTx{}, fmt.Errorf("nil output %d", i)
		}
		txType, payload, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if parsed.Type == 0 {
			parsed.Type = txType
			parsed.TemplateIndex = i
		} else if parsed.Type != txType {
			return ParsedTx{}, errors.New("transaction contains mixed template OP_RETURN output types")
		} else if txType != TxTypeDeploy && txType != TxTypeInvoke {
			return ParsedTx{}, errors.New("transaction contains multiple singleton template OP_RETURN outputs")
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
	case TxTypeDeploy:
		payload, err := DecodeDeployPayload(parsed.Payload)
		if err != nil {
			return ParsedTx{}, err
		}
		parsed.Deploy = &payload
	case TxTypeInvoke:
		payload, err := DecodeInvokePayload(parsed.Payload)
		if err != nil {
			return ParsedTx{}, err
		}
		parsed.Invoke = &payload
		outputs, err := FindInvokeContractOutputs(tx, resolver)
		if err != nil {
			return ParsedTx{}, err
		}
		parsed.ContractOutputs = outputs
	case TxTypeResult:
		return ParsedTx{}, errors.New("template RESULT transactions are built by block execution and are not accepted as external input")
	case TxTypeCoinbaseStateRoot:
		if len(payloadParts) != 1 {
			return ParsedTx{}, errors.New("template state root must use exactly one OP_RETURN")
		}
		payload, err := contractcommon.DecodeStateRootPayload(parsed.Payload)
		if err != nil {
			return ParsedTx{}, err
		}
		parsed.StateRoot = &payload
	default:
		return ParsedTx{}, fmt.Errorf("unsupported template tx type %d", parsed.Type)
	}
	return parsed, nil
}

func FindInvokeContractOutputs(tx *wire.MsgTx, resolver ContractScriptResolver) ([]ContractOutput, error) {
	if resolver == nil {
		return nil, errors.New("missing contract script resolver")
	}
	txid := tx.TxID()
	var contract *ContractAddress
	outputs := make([]ContractOutput, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return nil, fmt.Errorf("nil output %d", i)
		}
		addr, ok, err := resolver(txOut.PkScript)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if addr.ContractType() != ContractTypeTemplate {
			continue
		}
		if contract == nil {
			cp := addr
			contract = &cp
		} else if !contract.Equal(addr) {
			return nil, errors.New("template INVOKE outputs target multiple contract addresses")
		}
		vout := uint32(i)
		outputs = append(outputs, ContractOutput{
			OutPoint: OutPoint{TxID: txid, Vout: vout},
			Vout:     vout,
			Contract: addr,
			Value:    txOut.Value,
			Assets:   txOut.Assets.Clone(),
			PkScript: cloneBytes(txOut.PkScript),
		})
	}
	if len(outputs) == 0 {
		return nil, errors.New("template INVOKE has no contract output")
	}
	return outputs, nil
}

func FindContractOutputsForContract(tx *wire.MsgTx, resolver ContractScriptResolver, contract ContractAddress) ([]ContractOutput, error) {
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
		if !ok || !contract.Equal(addr) {
			continue
		}
		vout := uint32(i)
		outputs = append(outputs, ContractOutput{
			OutPoint: OutPoint{TxID: txid, Vout: vout},
			Vout:     vout,
			Contract: addr,
			Value:    txOut.Value,
			Assets:   txOut.Assets.Clone(),
			PkScript: cloneBytes(txOut.PkScript),
		})
	}
	return outputs, nil
}

func WireOutPointToTemplate(out wire.OutPoint) OutPoint {
	return OutPoint{
		TxID: out.Hash.String(),
		Vout: out.Index,
	}
}

func errUnexpectedTemplateTxType(txType TxType) error {
	return fmt.Errorf("unexpected template tx type %d", txType)
}

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
