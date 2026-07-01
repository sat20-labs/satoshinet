package evm

import (
	"errors"
	"strings"
)

const (
	DefaultMetadataQueryGas = int64(300000)
	DefaultMaxManagedAssets = 8
)

var (
	contractNameSelector      = methodSelector("contractName()")
	contractSubtypeSelector   = methodSelector("contractSubtype()")
	managedAssetCountSelector = methodSelector("managedAssetCount()")
	managedAssetSelector      = methodSelector("managedAsset(uint256)")
	legacyAssetNameSelector   = methodSelector("assetName()")
	legacyAssetANameSelector  = methodSelector("assetAName()")
	legacyAssetBNameSelector  = methodSelector("assetBName()")
	errEVMMetadataUnavailable = errors.New("EVM metadata unavailable")
)

type ContractMetadata struct {
	Name          string   `json:"name,omitempty"`
	Subtype       string   `json:"subtype,omitempty"`
	ManagedAssets []string `json:"assets,omitempty"`
}

func (m ContractMetadata) Empty() bool {
	return strings.TrimSpace(m.Name) == "" &&
		strings.TrimSpace(m.Subtype) == "" &&
		len(m.ManagedAssets) == 0
}

func QueryContractMetadata(state *MemoryStateDB, contract ContractAddress, block BlockContext,
	maxAssets int) (ContractMetadata, bool) {

	if state == nil {
		return ContractMetadata{}, false
	}
	if maxAssets <= 0 {
		maxAssets = DefaultMaxManagedAssets
	}
	target := ContractAddressHash(contract)
	caller := EVMAddress{}
	query := func(input []byte) ([]byte, error) {
		runtime := NewRuntime(state.Clone())
		result := runtime.Call(CallRequest{
			Caller: caller,
			Target: target,
			CallID: "metadata",
			Input:  input,
			Gas:    DefaultMetadataQueryGas,
			Block:  block,
		})
		if result.Err != nil || result.Status != ResultStatusSuccess {
			if result.Err != nil {
				return nil, result.Err
			}
			return nil, errEVMMetadataUnavailable
		}
		return result.ReturnData, nil
	}
	readString := func(selector [4]byte) (string, bool) {
		ret, err := query(selector[:])
		if err != nil {
			return "", false
		}
		value, err := abiReadString(ret, 0)
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(value), strings.TrimSpace(value) != ""
	}
	readUint := func(selector [4]byte) (uint64, bool) {
		ret, err := query(selector[:])
		if err != nil {
			return 0, false
		}
		value, err := abiReadUint64(ret, 0)
		return value, err == nil
	}

	var meta ContractMetadata
	if name, ok := readString(contractNameSelector); ok {
		meta.Name = name
	}
	if subtype, ok := readString(contractSubtypeSelector); ok {
		meta.Subtype = subtype
	}
	if count, ok := readUint(managedAssetCountSelector); ok {
		if count > uint64(maxAssets) {
			count = uint64(maxAssets)
		}
		for i := uint64(0); i < count; i++ {
			input := appendMethod(managedAssetSelector, abiEncodeUint64(i))
			ret, err := query(input)
			if err != nil {
				continue
			}
			asset, err := abiReadString(ret, 0)
			asset = strings.TrimSpace(asset)
			if err == nil && asset != "" && !metadataHasAsset(meta.ManagedAssets, asset) {
				meta.ManagedAssets = append(meta.ManagedAssets, asset)
			}
		}
	}

	for _, selector := range [][4]byte{legacyAssetNameSelector, legacyAssetANameSelector, legacyAssetBNameSelector} {
		if asset, ok := readString(selector); ok && !metadataHasAsset(meta.ManagedAssets, asset) {
			meta.ManagedAssets = append(meta.ManagedAssets, asset)
		}
	}
	if meta.Empty() {
		return ContractMetadata{}, false
	}
	return meta, true
}

func metadataHasAsset(assets []string, asset string) bool {
	for _, existing := range assets {
		if existing == asset {
			return true
		}
	}
	return false
}
