package evm

import "github.com/sat20-labs/satoshinet/wire"

type TxOrderInfo struct {
	IsEVM    bool
	Type     TxType
	GasLimit uint64
}

func ContractPrefixForNet(net wire.BitcoinNet) string {
	if net == wire.MainNet {
		return MainnetContractPrefix
	}
	return TestnetContractPrefix
}

func ClassifyTxForBlockOrder(tx *wire.MsgTx, contractPrefix string) (TxOrderInfo, error) {
	parsed, err := ParseTx(tx, StandardContractScriptResolver(contractPrefix))
	if err != nil {
		return TxOrderInfo{}, err
	}
	if parsed.Type == 0 {
		return TxOrderInfo{}, nil
	}

	info := TxOrderInfo{
		IsEVM: true,
		Type:  parsed.Type,
	}
	switch parsed.Type {
	case TxTypeDeploy:
		hasOutput, err := hasEVMContractOutput(tx, contractPrefix)
		if err != nil || !hasOutput {
			return TxOrderInfo{}, err
		}
		if parsed.Deploy != nil {
			info.GasLimit = parsed.Deploy.GasLimit
		}
	case TxTypeInvoke:
		if parsed.Invoke != nil {
			info.GasLimit = parsed.Invoke.GasLimit
		}
	}
	return info, nil
}

func hasEVMContractOutput(tx *wire.MsgTx, contractPrefix string) (bool, error) {
	resolver := StandardContractScriptResolver(contractPrefix)
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
