package template

import (
	"crypto/sha256"
	"encoding/json"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

type RuntimeStore struct {
	runtimes map[string]*ContractRuntime
}

type RuntimeSnapshot struct {
	Address      string               `json:"address"`
	TemplateName string               `json:"templateName"`
	Version      uint32               `json:"version"`
	State        TemplateRuntimeState `json:"state"`
}

func NewRuntimeStore() *RuntimeStore {
	return &RuntimeStore{runtimes: make(map[string]*ContractRuntime)}
}

func (s *RuntimeStore) Add(runtime *ContractRuntime) {
	if s.runtimes == nil {
		s.runtimes = make(map[string]*ContractRuntime)
	}
	s.runtimes[runtime.URL()] = runtime
}

func (s *RuntimeStore) Clone() *RuntimeStore {
	encoded, err := s.MarshalBinary()
	if err != nil {
		return NewRuntimeStore()
	}
	clone, err := DecodeRuntimeStore(encoded, nil)
	if err != nil {
		return NewRuntimeStore()
	}
	return clone
}

func (s *RuntimeStore) Get(contract ContractAddress) (*ContractRuntime, bool) {
	if s == nil {
		return nil, false
	}
	runtime, ok := s.runtimes[contract.EncodeAddress()]
	return runtime, ok
}

func (s *RuntimeStore) Exists(contract ContractAddress) bool {
	_, ok := s.Get(contract)
	return ok
}

func (s *RuntimeStore) Snapshots() ([]RuntimeSnapshot, error) {
	keys := s.sortedKeys()
	out := make([]RuntimeSnapshot, 0, len(keys))
	for _, key := range keys {
		runtime := s.runtimes[key]
		if runtime == nil {
			continue
		}
		state, err := runtime.RuntimeState()
		if err != nil {
			return nil, err
		}
		address := runtime.Address()
		out = append(out, RuntimeSnapshot{
			Address:      address.EncodeAddress(),
			TemplateName: runtime.TemplateName(),
			Version:      runtime.Version(),
			State:        cloneRuntimeState(state),
		})
	}
	return out, nil
}

func (s *RuntimeStore) SettleBlock(height int64) ([]*SettlementPlan, error) {
	return s.SettleBlockWithGasConfig(height, DefaultGasConfig())
}

func (s *RuntimeStore) SettleBlockWithGasConfig(height int64, gasConfig GasConfig) ([]*SettlementPlan, error) {
	if s == nil {
		return nil, nil
	}
	keys := make([]string, 0, len(s.runtimes))
	for key := range s.runtimes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	plans := make([]*SettlementPlan, 0)
	for _, key := range keys {
		plan, err := s.runtimes[key].SettleBlockWithGasConfig(height, gasConfig)
		if err != nil {
			return nil, err
		}
		if settlementPlanHasChanges(plan) {
			plans = append(plans, plan)
		}
	}
	return plans, nil
}

func (s *RuntimeStore) ReconcileAssetCaches(contractUTXOs ContractUTXOProvider, gasConfig GasConfig) error {
	if s == nil || contractUTXOs == nil {
		return nil
	}
	gasAssetName := gasConfig.Normalize().GasAssetName
	for _, key := range s.sortedKeys() {
		runtime := s.runtimes[key]
		if runtime == nil {
			continue
		}
		utxos, err := contractUTXOs(runtime.Address())
		if err != nil {
			return err
		}
		state, err := runtime.loadRuntimeState()
		if err != nil {
			return err
		}
		if !reconcileRunningAssetCache(runtime.Contract(), &state.Running, utxos, gasAssetName) {
			continue
		}
		if !state.Running.TradingReady {
			state.Running.TradingReady = state.Running.ammTradingReady()
		}
		if err := runtime.saveRuntimeState(state); err != nil {
			return err
		}
	}
	return nil
}

func reconcileRunningAssetCache(contract Contract, running *RunningData, utxos []UTXO, gasAssetName string) bool {
	if running == nil {
		return false
	}
	assetA, assetB, ok := runtimePoolAssets(contract)
	if !ok {
		return false
	}
	assetAAmount, assetAOK := sumUTXOAssetAmount(utxos, assetA)
	assetBAmount, assetBOK := sumUTXOAssetAmount(utxos, assetB)
	changed := false
	if assetAOK && !decimalEqualAllowNil(running.AssetAInPool, assetAAmount) {
		running.AssetAInPool = assetAAmount
		changed = true
	}
	if assetBOK && !decimalEqualAllowNil(running.AssetBInPool, assetBAmount) {
		running.AssetBInPool = assetBAmount
		changed = true
	}
	if gasAssetName != "" && gasAssetName != assetA && gasAssetName != assetB {
		gasAmount, ok := sumUTXOAssetAmount(utxos, gasAssetName)
		if ok && !decimalEqualAllowNil(running.GasBalance, gasAmount) {
			running.GasBalance = gasAmount
			changed = true
		}
	}
	return changed
}

func runtimePoolAssets(contract Contract) (assetA, assetB string, ok bool) {
	switch c := contract.(type) {
	case *AMMContract:
		return c.AssetName, SatoshiAssetName, true
	case *ExchangeContract:
		return c.AssetAName, c.AssetBName, true
	default:
		return "", "", false
	}
}

func sumUTXOAssetAmount(utxos []UTXO, assetName string) (*scommon.Decimal, bool) {
	if assetName == "" {
		return nil, false
	}
	total, err := contractframework.SumUTXOAssetAmount(utxos, assetName)
	if err != nil {
		return nil, false
	}
	return total, true
}

func decimalEqualAllowNil(a, b *scommon.Decimal) bool {
	if a == nil {
		a = parseDecimalOrZero("0")
	}
	if b == nil {
		b = parseDecimalOrZero("0")
	}
	return a.Cmp(b) == 0
}

func (s *RuntimeStore) StateRoot() [32]byte {
	h := sha256.New()
	if s == nil {
		var root [32]byte
		copy(root[:], h.Sum(nil))
		return root
	}
	keys := make([]string, 0, len(s.runtimes))
	for key := range s.runtimes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeLengthPrefixed(h, []byte(key))
		root := s.runtimes[key].StateRoot()
		writeLengthPrefixed(h, root[:])
	}
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

func (s *RuntimeStore) sortedKeys() []string {
	keys := make([]string, 0)
	if s != nil {
		keys = make([]string, 0, len(s.runtimes))
		for key := range s.runtimes {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func cloneRuntimeState(state TemplateRuntimeState) TemplateRuntimeState {
	encoded, err := json.Marshal(state)
	if err != nil {
		return TemplateRuntimeState{}
	}
	var out TemplateRuntimeState
	if err := json.Unmarshal(encoded, &out); err != nil {
		return TemplateRuntimeState{}
	}
	return out
}
