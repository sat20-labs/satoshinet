package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

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

func (s *RuntimeStore) ActiveNetworkExclusiveExists(runtime *ContractRuntime) bool {
	if s == nil || runtime == nil {
		return false
	}
	if !runtime.Contract().NetworkExclusive() {
		return false
	}
	want := runtime.NetworkExclusiveKey()
	if want == "" {
		return false
	}
	for _, existing := range s.runtimes {
		if existing == nil {
			continue
		}
		if existing.NetworkExclusiveKey() != want {
			continue
		}
		if !existing.NetworkExclusiveActive() {
			continue
		}
		return true
	}
	return false
}

func (r *ContractRuntime) NetworkExclusiveKey() string {
	if r == nil || r.base == nil {
		return ""
	}
	sum := sha256.Sum256(r.base.contractContent)
	return r.base.templateName + ":" + strconv.FormatUint(uint64(r.base.templateVersion), 10) + ":" +
		hex.EncodeToString(sum[:])
}

func (r *ContractRuntime) NetworkExclusiveActive() bool {
	if r == nil {
		return false
	}
	state, err := r.RuntimeState()
	if err != nil {
		return false
	}
	return !state.ClosedForContract(r.Contract())
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
	return s.SettleBlockWithGasConfigAndPrecision(height, gasConfig, nil)
}

func (s *RuntimeStore) SettleBlockWithGasConfigAndPrecision(height int64, gasConfig GasConfig,
	assetPrecision contractframework.AssetPrecisionResolver) ([]*SettlementPlan, error) {

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
		runtimeGasConfig := GasConfigForRuntime(gasConfig, s.runtimes[key])
		plan, err := s.runtimes[key].SettleBlockWithGasConfigAndPrecision(height, runtimeGasConfig, assetPrecision)
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
	return s.reconcileAssetCaches(contractUTXOs, gasConfig, false)
}

func (s *RuntimeStore) reconcileAssetCaches(contractUTXOs ContractUTXOProvider, gasConfig GasConfig, skipEmpty bool) error {
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
		if skipEmpty && len(utxos) == 0 {
			continue
		}
		state, err := runtime.loadRuntimeState()
		if err != nil {
			return err
		}
		if !reconcileRunningAssetCache(runtime.Contract(), &state, utxos, gasAssetName) {
			continue
		}
		if amm, ok := runtime.Contract().(*AMMContract); ok {
			_ = amm
			running := state.AMMData()
			if !running.TradingReady {
				running.TradingReady = running.ammTradingReady()
			}
		}
		if err := runtime.saveRuntimeState(state); err != nil {
			return err
		}
	}
	return nil
}

func reconcileRunningAssetCache(contract Contract, state *TemplateRuntimeState, utxos []UTXO, gasAssetName string) bool {
	if state == nil {
		return false
	}
	assetA, assetB, ok := runtimePoolAssets(contract)
	if !ok {
		return false
	}
	utxos = filterPendingItemUTXOs(state.Items, utxos)
	assetAAmount, assetAOK := sumUTXOAssetAmount(utxos, assetA)
	assetBAmount, assetBOK := sumUTXOAssetAmount(utxos, assetB)
	changed := false
	getPools := func() (*scommon.Decimal, *scommon.Decimal, *scommon.Decimal) {
		switch contract.(type) {
		case *AMMContract:
			running := state.AMMData()
			return running.AssetAInPool, running.AssetBInPool, running.GasBalance
		case *ExchangeContract:
			running := state.ExchangeData()
			return running.AssetAInPool, running.AssetBInPool, running.GasBalance
		default:
			return nil, nil, nil
		}
	}
	setAssetA := func(v *scommon.Decimal) {
		switch contract.(type) {
		case *AMMContract:
			state.AMMData().AssetAInPool = v
		case *ExchangeContract:
			state.ExchangeData().AssetAInPool = v
		}
	}
	setAssetB := func(v *scommon.Decimal) {
		switch contract.(type) {
		case *AMMContract:
			state.AMMData().AssetBInPool = v
		case *ExchangeContract:
			state.ExchangeData().AssetBInPool = v
		}
	}
	setGas := func(v *scommon.Decimal) {
		switch contract.(type) {
		case *AMMContract:
			state.AMMData().GasBalance = v
		case *ExchangeContract:
			state.ExchangeData().GasBalance = v
		}
	}
	currentA, currentB, currentGas := getPools()
	if assetAOK && !decimalEqualAllowNil(currentA, assetAAmount) {
		setAssetA(assetAAmount)
		changed = true
	}
	if assetBOK && !decimalEqualAllowNil(currentB, assetBAmount) {
		setAssetB(assetBAmount)
		changed = true
	}
	if gasAssetName != "" && gasAssetName != assetA && gasAssetName != assetB {
		gasAmount, ok := sumUTXOAssetAmount(utxos, gasAssetName)
		if ok && !decimalEqualAllowNil(currentGas, gasAmount) {
			setGas(gasAmount)
			changed = true
		}
	}
	return changed
}

func filterPendingItemUTXOs(items []InvokeItem, utxos []UTXO) []UTXO {
	if len(items) == 0 || len(utxos) == 0 {
		return utxos
	}
	pending := make(map[OutPoint]struct{})
	for i := range items {
		item := &items[i]
		if item == nil || item.Finished() || item.InUtxos == "" {
			continue
		}
		for _, raw := range strings.Split(item.InUtxos, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			outpoint, err := ParseOutPoint(raw)
			if err != nil {
				continue
			}
			pending[WireOutPointToTemplate(outpoint)] = struct{}{}
		}
	}
	if len(pending) == 0 {
		return utxos
	}
	out := make([]UTXO, 0, len(utxos))
	for _, utxo := range utxos {
		if _, ok := pending[utxo.OutPoint]; ok {
			continue
		}
		out = append(out, utxo)
	}
	return out
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
