package node

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/blockchain"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func validationContractUTXOProvider(view blockchain.ContractAssetIndexView) ContractUTXOProvider {
	if view == nil {
		return nil
	}
	return func(contract contractcommon.ContractAddress) ([]ContractUTXO, error) {
		address := contract.MustEncode()
		byAsset := view.GetInternalAssetUTXOsInAddress(address)
		utxos := make([]ContractUTXO, 0)
		seen := make(map[string]struct{})
		for _, outputs := range byAsset {
			for _, output := range outputs {
				if output == nil {
					continue
				}
				if _, ok := seen[output.OutPointStr]; ok {
					continue
				}
				seen[output.OutPointStr] = struct{}{}
				outpoint, err := wire.NewOutPointFromString(output.OutPointStr)
				if err != nil {
					return nil, err
				}
				if output.OutValue.Value < 0 {
					return nil, fmt.Errorf("negative contract output value")
				}
				utxos = append(utxos, ContractUTXO{
					OutPoint: *outpoint,
					Value:    output.OutValue.Value,
					Assets:   output.OutValue.Assets.Clone(),
					Height:   int64(output.Height()),
				})
			}
		}
		return utxos, nil
	}
}

func validationAssetPrecisionResolver(view blockchain.ContractAssetIndexView) AssetPrecisionResolver {
	if view == nil {
		return nil
	}
	return func(assetName string) (int, bool) {
		name := wire.NewAssetNameFromString(assetName)
		if name == nil {
			return 0, false
		}
		info := view.GetInternalTickerInfo(name)
		if info == nil {
			return 0, false
		}
		return info.Divisibility, true
	}
}

// applyContractAssetIndexView temporarily replaces only the block validator's
// asset providers. Mining, mempool and RPC services keep their original live
// IndexerMgr providers, so candidate-branch state cannot leak outside this
// serialized validation call.
func (v *CompositeContractBlockValidator) applyContractAssetIndexView(
	assetView blockchain.ContractAssetIndexView) (func(), error) {

	if assetView == nil {
		return func() {}, nil
	}
	base := validationContractUTXOProvider(assetView)
	precision := validationAssetPrecisionResolver(assetView)
	restores := make([]func(), 0, 3)

	if v.cfg.TemplateValidator != nil {
		validator, ok := v.cfg.TemplateValidator.(*TemplateBlockExecutionValidator)
		if !ok {
			return nil, fmt.Errorf("template validator does not support branch AIDX override")
		}
		oldUTXOs, oldPrecision := validator.cfg.ContractUTXOs, validator.cfg.AssetPrecision
		validator.cfg.ContractUTXOs = templateContractUTXOProvider(base)
		validator.cfg.AssetPrecision = precision
		restores = append(restores, func() {
			validator.cfg.ContractUTXOs, validator.cfg.AssetPrecision = oldUTXOs, oldPrecision
		})
	}
	if v.cfg.EVMValidator != nil {
		validator, ok := v.cfg.EVMValidator.(*EVMBlockExecutionValidator)
		if !ok {
			for i := len(restores) - 1; i >= 0; i-- {
				restores[i]()
			}
			return nil, fmt.Errorf("EVM validator does not support branch AIDX override")
		}
		oldUTXOs, oldPrecision := validator.cfg.ContractUTXOs, validator.cfg.AssetPrecision
		validator.cfg.ContractUTXOs = evmContractUTXOProvider(base)
		validator.cfg.AssetPrecision = precision
		restores = append(restores, func() {
			validator.cfg.ContractUTXOs, validator.cfg.AssetPrecision = oldUTXOs, oldPrecision
		})
	}
	if v.cfg.AgentValidator != nil {
		validator, ok := v.cfg.AgentValidator.(*AgentBlockExecutionValidator)
		if !ok {
			for i := len(restores) - 1; i >= 0; i-- {
				restores[i]()
			}
			return nil, fmt.Errorf("agent validator does not support branch AIDX override")
		}
		oldUTXOs, oldPrecision := validator.cfg.ContractUTXOs, validator.cfg.AssetPrecision
		validator.cfg.ContractUTXOs = agentContractUTXOProvider(base)
		validator.cfg.AssetPrecision = precision
		restores = append(restores, func() {
			validator.cfg.ContractUTXOs, validator.cfg.AssetPrecision = oldUTXOs, oldPrecision
		})
	}

	return func() {
		for i := len(restores) - 1; i >= 0; i-- {
			restores[i]()
		}
	}, nil
}
