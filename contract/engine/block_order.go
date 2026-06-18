package engine

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractapi "github.com/sat20-labs/satoshinet/contract"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	PriorityTemplate = 1
	PriorityEVM      = 2
	PriorityAgent    = 3
)

type TxClass struct {
	ContractType byte
	TxType       contractcommon.TxType
	Priority     int
	GasLimit     int64
}

func (c TxClass) IsWork() bool {
	return c.TxType == contractcommon.TxTypeDeploy ||
		c.TxType == contractcommon.TxTypeInvoke
}

func (c TxClass) IsResult() bool {
	return c.TxType == contractcommon.TxTypeResult
}

func ClassifyTxForBlockOrder(tx *wire.MsgTx, params *chaincfg.Params) (TxClass, bool, error) {
	prefix := contractPrefixForParams(params)
	return ClassifyTxForBlockOrderWithPrefix(tx, prefix)
}

func ClassifyTxForBlockOrderWithPrefix(tx *wire.MsgTx, prefix string) (TxClass, bool, error) {
	txType, payload, found, err := collectBlockOrderPayload(tx)
	if err != nil {
		return TxClass{}, false, err
	}
	if !found {
		return classifyDefaultInvokeForBlockOrder(tx, prefix)
	}
	contractType := inferContractTypeFromOutputs(tx, prefix)
	if contractType == 0 && (txType == contractcommon.TxTypeResult ||
		txType == contractcommon.TxTypeCoinbaseStateRoot) {
		contractType = contractcommon.ContractTypeTemplate
	}
	if contractType == 0 {
		return TxClass{}, false, nil
	}
	class := TxClass{
		ContractType: contractType,
		TxType:       txType,
		Priority:     priorityForContractType(contractType),
	}
	switch txType {
	case contractcommon.TxTypeDeploy:
		gasLimit, err := deployGasLimit(contractType, payload)
		if err != nil {
			return TxClass{}, false, err
		}
		class.GasLimit = gasLimit
	case contractcommon.TxTypeInvoke:
		gasLimit, err := invokeGasLimit(contractType, payload)
		if err != nil {
			return TxClass{}, false, err
		}
		class.GasLimit = gasLimit
	case contractcommon.TxTypeResult, contractcommon.TxTypeCoinbaseStateRoot:
	default:
		return TxClass{}, false, fmt.Errorf("unsupported contract tx type %d", txType)
	}
	return class, true, nil
}

func ClassifyTxPayloadType(tx *wire.MsgTx) (contractcommon.TxType, bool, error) {
	if tx == nil {
		return 0, false, fmt.Errorf("missing transaction")
	}
	var txType contractcommon.TxType
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return 0, false, fmt.Errorf("nil output %d", i)
		}
		nextType, _, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if txType == 0 {
			txType = nextType
			continue
		}
		if txType != nextType {
			return 0, false, fmt.Errorf("transaction contains mixed contract OP_RETURN output types")
		}
		if txType != contractcommon.TxTypeDeploy && txType != contractcommon.TxTypeInvoke {
			return 0, false, fmt.Errorf("transaction contains multiple singleton contract OP_RETURN outputs")
		}
	}
	if txType == 0 {
		return 0, false, nil
	}
	return txType, true, nil
}

func collectBlockOrderPayload(tx *wire.MsgTx) (contractcommon.TxType, []byte, bool, error) {
	if tx == nil {
		return 0, nil, false, fmt.Errorf("missing transaction")
	}
	var txType contractcommon.TxType
	payload := make([]byte, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return 0, nil, false, fmt.Errorf("nil output %d", i)
		}
		nextType, content, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if txType == 0 {
			txType = nextType
		} else if txType != nextType {
			return 0, nil, false, fmt.Errorf("transaction contains mixed contract OP_RETURN output types")
		} else if txType != contractcommon.TxTypeDeploy && txType != contractcommon.TxTypeInvoke {
			return 0, nil, false, fmt.Errorf("transaction contains multiple singleton contract OP_RETURN outputs")
		}
		payload = append(payload, content...)
	}
	return txType, payload, txType != 0, nil
}

func classifyDefaultInvokeForBlockOrder(tx *wire.MsgTx, prefix string) (TxClass, bool, error) {
	for _, contractType := range []byte{
		contractcommon.ContractTypeTemplate,
		contractcommon.ContractTypeEVM,
		contractcommon.ContractTypeAgent,
	} {
		outputs, err := contractcommon.FindDefaultInvokeOutputs(tx, prefix, contractType)
		if err != nil {
			return TxClass{}, false, err
		}
		if len(outputs) == 0 {
			continue
		}
		return TxClass{
			ContractType: contractType,
			TxType:       contractcommon.TxTypeInvoke,
			Priority:     priorityForContractType(contractType),
			GasLimit:     int64(contractcommon.InvokeBaseGas),
		}, true, nil
	}
	return TxClass{}, false, nil
}

func deployGasLimit(contractType byte, payload []byte) (int64, error) {
	switch contractType {
	case contractcommon.ContractTypeTemplate:
		deploy, err := contractcommon.DecodeDeployPayload(payload)
		return gasLimitFromDecoded(deploy.GasLimit, err)
	case contractcommon.ContractTypeAgent:
		deploy, err := contractcommon.DecodeDeployPayload(payload)
		return gasLimitFromDecoded(deploy.GasLimit, err)
	case contractcommon.ContractTypeEVM:
		deploy, err := contractcommon.DecodeDeployPayload(payload)
		return gasLimitFromDecoded(deploy.GasLimit, err)
	default:
		return 0, fmt.Errorf("unknown contract type %d", contractType)
	}
}

func invokeGasLimit(contractType byte, payload []byte) (int64, error) {
	switch contractType {
	case contractcommon.ContractTypeTemplate:
		invoke, err := contractcommon.DecodeInvokePayload(payload)
		return gasLimitFromDecoded(invoke.GasLimit, err)
	case contractcommon.ContractTypeAgent:
		invoke, err := contractcommon.DecodeInvokePayload(payload)
		return gasLimitFromDecoded(invoke.GasLimit, err)
	case contractcommon.ContractTypeEVM:
		invoke, err := contractcommon.DecodeInvokePayload(payload)
		return gasLimitFromDecoded(invoke.GasLimit, err)
	default:
		return 0, fmt.Errorf("unknown contract type %d", contractType)
	}
}

func gasLimitFromDecoded(gas int64, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return gas, nil
}

func priorityForContractType(contractType byte) int {
	switch contractType {
	case contractcommon.ContractTypeTemplate:
		return PriorityTemplate
	case contractcommon.ContractTypeEVM:
		return PriorityEVM
	case contractcommon.ContractTypeAgent:
		return PriorityAgent
	default:
		return 0
	}
}

func BlockHasWork(txs []*btcutil.Tx, params *chaincfg.Params) bool {
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		class, found, err := ClassifyTxForBlockOrder(tx.MsgTx(), params)
		if err == nil && found && class.IsWork() {
			return true
		}
	}
	return false
}

func BlockHasContractTypeWork(txs []*btcutil.Tx, params *chaincfg.Params, contractType byte) bool {
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		class, found, err := ClassifyTxForBlockOrder(tx.MsgTx(), params)
		if err == nil && found && class.ContractType == contractType && class.IsWork() {
			return true
		}
		if defaultTxHasContractTypeWork(tx.MsgTx(), params, contractType) {
			return true
		}
	}
	return false
}

func DefaultInvokeContractTypes(tx *wire.MsgTx, params *chaincfg.Params) (map[byte]struct{}, error) {
	prefix := contractPrefixForParams(params)
	out := make(map[byte]struct{})
	for _, contractType := range []byte{
		contractcommon.ContractTypeTemplate,
		contractcommon.ContractTypeEVM,
		contractcommon.ContractTypeAgent,
	} {
		outputs, err := contractcommon.FindDefaultInvokeOutputs(tx, prefix, contractType)
		if err != nil {
			return nil, err
		}
		if len(outputs) != 0 {
			out[contractType] = struct{}{}
		}
	}
	return out, nil
}

func defaultTxHasContractTypeWork(tx *wire.MsgTx, params *chaincfg.Params, contractType byte) bool {
	types, err := DefaultInvokeContractTypes(tx, params)
	if err != nil {
		return false
	}
	_, ok := types[contractType]
	return ok
}

func CheckBlockOrder(block *btcutil.Block, params *chaincfg.Params) error {
	if block == nil || len(block.Transactions()) == 0 {
		return nil
	}
	coinbaseTx := block.Transactions()[0].MsgTx()
	if _, _, err := contractapi.FindCoinbaseStateRoot(coinbaseTx); err != nil {
		return fmt.Errorf("block contains invalid contract state root in coinbase: %w", err)
	}

	seenContract := false
	seenResult := false
	highestWorkPriority := 0
	for i, tx := range block.Transactions()[1:] {
		class, found, err := ClassifyTxForBlockOrder(tx.MsgTx(), params)
		if err != nil {
			return fmt.Errorf("block contains malformed contract transaction %v at index %d: %w",
				tx.Hash(), i+1, err)
		}
		if !found {
			if seenContract {
				return fmt.Errorf("block contains non-contract transaction %v after contract transactions at index %d",
					tx.Hash(), i+1)
			}
			continue
		}
		if class.TxType == contractcommon.TxTypeCoinbaseStateRoot {
			return fmt.Errorf("block contains contract state root outside coinbase at index %d", i+1)
		}
		if seenResult && !class.IsResult() {
			return fmt.Errorf("block contains contract transaction %v after RESULT transactions at index %d",
				tx.Hash(), i+1)
		}
		if class.IsResult() {
			seenResult = true
			seenContract = true
			continue
		}
		if class.Priority < highestWorkPriority {
			return fmt.Errorf("block contains contract transaction %v with priority %d after priority %d at index %d",
				tx.Hash(), class.Priority, highestWorkPriority, i+1)
		}
		if class.Priority > highestWorkPriority {
			highestWorkPriority = class.Priority
		}
		seenContract = true
	}
	return nil
}

func contractPrefixForParams(params *chaincfg.Params) string {
	if params == nil {
		return contractcommon.TestnetContractPrefix
	}
	if params.Net == wire.MainNet {
		return contractcommon.MainnetContractPrefix
	}
	return contractcommon.TestnetContractPrefix
}
