package contract

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/contract/evm"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
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
	GasLimit     uint64
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
	templateInfo, templateErr := tmplcontract.ClassifyTxForBlockOrder(tx, prefix)
	if templateErr == nil && templateInfo.IsTemplate {
		return TxClass{
			ContractType: contractcommon.ContractTypeTemplate,
			TxType:       contractcommon.TxType(templateInfo.Type),
			Priority:     PriorityTemplate,
			GasLimit:     templateInfo.GasLimit,
		}, true, nil
	}

	info, err := evm.ClassifyTxForBlockOrder(tx, prefix)
	if err != nil {
		if templateErr != nil {
			return TxClass{}, false, templateErr
		}
		return TxClass{}, false, err
	}
	if info.IsEVM {
		return TxClass{
			ContractType: contractcommon.ContractTypeEVM,
			TxType:       contractcommon.TxType(info.Type),
			Priority:     PriorityEVM,
			GasLimit:     info.GasLimit,
		}, true, nil
	}
	return TxClass{}, false, nil
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
	}
	return false
}

func CheckBlockOrder(block *btcutil.Block, params *chaincfg.Params) error {
	if block == nil || len(block.Transactions()) == 0 {
		return nil
	}
	coinbaseTx := block.Transactions()[0].MsgTx()
	if _, _, err := FindCoinbaseStateRoot(coinbaseTx); err != nil {
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
