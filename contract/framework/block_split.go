package framework

import (
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type BlockContractSplit struct {
	WorkTxs     map[ModuleType][]*wire.MsgTx
	ResultTxs   map[ModuleType][]*wire.MsgTx
	WorkOutputs map[wire.OutPoint]contract.ContractAddress
}

type UTXOView interface {
	LookupContractAddress(outpoint wire.OutPoint, prefix string) (contract.ContractAddress, bool, error)
}

type SplitRequest struct {
	Txs        []*wire.MsgTx
	Prefix     string
	Modules    []Module
	ParentView UTXOView
}

func SplitBlockContractTxs(req SplitRequest) (BlockContractSplit, error) {
	prefix := req.Prefix
	if prefix == "" {
		prefix = contract.TestnetContractPrefix
	}
	split := BlockContractSplit{
		WorkTxs:     make(map[ModuleType][]*wire.MsgTx),
		ResultTxs:   make(map[ModuleType][]*wire.MsgTx),
		WorkOutputs: make(map[wire.OutPoint]contract.ContractAddress),
	}
	modules := make(map[ModuleType]Module, len(req.Modules))
	for _, module := range req.Modules {
		if module == nil {
			continue
		}
		modules[module.Type()] = module
	}

	for txIndex, tx := range req.Txs {
		if tx == nil {
			return split, fmt.Errorf("nil transaction %d", txIndex)
		}
		txType, foundPayload, err := contract.ClassifyTxPayloadType(tx)
		if err != nil {
			return split, fmt.Errorf("classify contract payload %s: %w", tx.TxID(), err)
		}
		if foundPayload && txType == contract.TxTypeCoinbaseStateRoot {
			continue
		}
		if foundPayload && txType == contract.TxTypeResult {
			moduleType, err := inferResultModule(tx, prefix, req.ParentView, split.WorkOutputs)
			if err != nil {
				return split, fmt.Errorf("classify CONTRACT_RESULT %s: %w", tx.TxID(), err)
			}
			if _, ok := modules[moduleType]; !ok {
				return split, fmt.Errorf("CONTRACT_RESULT %s belongs to unregistered module %d",
					tx.TxID(), moduleType)
			}
			split.ResultTxs[moduleType] = append(split.ResultTxs[moduleType], tx)
			continue
		}
		if foundPayload {
			class, found, err := classifyWithModules(tx, prefix, req.Modules)
			if err != nil {
				return split, err
			}
			if !found {
				continue
			}
			if !class.IsWork() {
				continue
			}
			moduleType := class.ContractType
			split.WorkTxs[moduleType] = append(split.WorkTxs[moduleType], tx)
			addWorkOutputs(split.WorkOutputs, tx, prefix, moduleType)
			continue
		}
		defaultTypes, err := defaultInvokeModuleTypes(tx, prefix, modules)
		if err != nil {
			return split, fmt.Errorf("classify default contract invoke %s: %w", tx.TxID(), err)
		}
		if len(defaultTypes) == 0 {
			continue
		}
		for _, moduleType := range defaultTypes {
			split.WorkTxs[moduleType] = append(split.WorkTxs[moduleType], tx)
		}
		addAllContractOutputs(split.WorkOutputs, tx, prefix)
	}
	return split, nil
}

func classifyWithModules(tx *wire.MsgTx, prefix string, modules []Module) (TxClass, bool, error) {
	var firstErr error
	for _, module := range modules {
		if module == nil {
			continue
		}
		class, found, err := module.ClassifyTx(tx, prefix)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if !found {
			continue
		}
		if class.ContractType == 0 {
			class.ContractType = module.Type()
		}
		return class, true, nil
	}
	if firstErr != nil {
		return TxClass{}, false, firstErr
	}
	return TxClass{}, false, nil
}

func inferResultModule(tx *wire.MsgTx, prefix string, parent UTXOView,
	workOutputs map[wire.OutPoint]contract.ContractAddress) (ModuleType, error) {

	var moduleType ModuleType
	found := false
	for inputIndex, txIn := range tx.TxIn {
		if txIn == nil {
			return 0, fmt.Errorf("nil input %d", inputIndex)
		}
		contractAddr, ok, err := lookupContractInput(txIn.PreviousOutPoint, prefix, parent, workOutputs)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		nextType := ModuleType(contractAddr.ContractType())
		if !found {
			moduleType = nextType
			found = true
			continue
		}
		if moduleType != nextType {
			return 0, fmt.Errorf("CONTRACT_RESULT spends contract UTXOs from multiple modules")
		}
	}
	if !found {
		return 0, fmt.Errorf("CONTRACT_RESULT spends no contract UTXO")
	}
	return moduleType, nil
}

func lookupContractInput(outpoint wire.OutPoint, prefix string, parent UTXOView,
	workOutputs map[wire.OutPoint]contract.ContractAddress) (contract.ContractAddress, bool, error) {

	if contractAddr, ok := workOutputs[outpoint]; ok {
		return contractAddr, true, nil
	}
	if parent == nil {
		return contract.ContractAddress{}, false, nil
	}
	return parent.LookupContractAddress(outpoint, prefix)
}

func defaultInvokeModuleTypes(tx *wire.MsgTx, prefix string,
	modules map[ModuleType]Module) ([]ModuleType, error) {

	seen := make(map[ModuleType]struct{})
	for _, contractType := range []ModuleType{ModuleTemplate, ModuleEVM, ModuleAgent} {
		if _, ok := modules[contractType]; !ok {
			continue
		}
		outputs, err := contract.FindDefaultInvokeOutputs(tx, prefix, byte(contractType))
		if err != nil {
			return nil, err
		}
		if len(outputs) != 0 {
			seen[contractType] = struct{}{}
		}
	}
	out := make([]ModuleType, 0, len(seen))
	for _, contractType := range []ModuleType{ModuleTemplate, ModuleEVM, ModuleAgent} {
		if _, ok := seen[contractType]; ok {
			out = append(out, contractType)
		}
	}
	return out, nil
}

func addWorkOutputs(out map[wire.OutPoint]contract.ContractAddress, tx *wire.MsgTx,
	prefix string, moduleType ModuleType) {

	for outpoint, contractAddr := range contractOutputs(tx, prefix) {
		if ModuleType(contractAddr.ContractType()) == moduleType {
			out[outpoint] = contractAddr
		}
	}
}

func addAllContractOutputs(out map[wire.OutPoint]contract.ContractAddress, tx *wire.MsgTx,
	prefix string) {

	for outpoint, contractAddr := range contractOutputs(tx, prefix) {
		out[outpoint] = contractAddr
	}
}

func contractOutputs(tx *wire.MsgTx, prefix string) map[wire.OutPoint]contract.ContractAddress {
	outputs := make(map[wire.OutPoint]contract.ContractAddress)
	hash := tx.TxHash()
	for index, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		contractAddr, ok, err := contract.ParseContractPkScript(txOut.PkScript, prefix)
		if err != nil || !ok {
			continue
		}
		outputs[wire.OutPoint{Hash: hash, Index: uint32(index)}] = contractAddr
	}
	return outputs
}
