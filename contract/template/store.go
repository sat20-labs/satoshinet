package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
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

// Clone forks mutable execution state. Contract descriptors are not mutated
// by block execution and can be shared with the candidate store.
func (s *RuntimeStore) Clone() *RuntimeStore {
	out := NewRuntimeStore()
	if s == nil {
		return out
	}
	for key, runtime := range s.runtimes {
		if runtime == nil {
			out.runtimes[key] = nil
			continue
		}
		cloned := *runtime
		if runtime.base != nil {
			base := *runtime.base
			base.contractContent = contractframework.CloneBytes(runtime.base.contractContent)
			base.managed = runtime.base.managed.Clone()
			base.state = make(map[string][]byte, len(runtime.base.state))
			for stateKey, value := range runtime.base.state {
				base.state[stateKey] = contractframework.CloneBytes(value)
			}
			cloned.base = &base
		}
		out.runtimes[key] = &cloned
	}
	return out
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
	if s == nil || runtime == nil || !runtime.Contract().NetworkExclusive() {
		return false
	}
	want := runtime.NetworkExclusiveKey()
	if want == "" {
		return false
	}
	for _, existing := range s.runtimes {
		if existing != nil && existing.NetworkExclusiveKey() == want && existing.NetworkExclusiveActive() {
			return true
		}
	}
	return false
}

func (r *ContractRuntime) NetworkExclusiveKey() string {
	if r == nil || r.base == nil {
		return ""
	}
	sum := sha256.Sum256(r.base.contractContent)
	return r.base.templateName + ":" + strconv.FormatUint(uint64(r.base.templateVersion), 10) + ":" + hex.EncodeToString(sum[:])
}

func (r *ContractRuntime) NetworkExclusiveActive() bool {
	if r == nil {
		return false
	}
	state, err := r.RuntimeState()
	if err != nil {
		return true // Corruption cannot free an occupied deployment slot.
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
			Address: address.EncodeAddress(), TemplateName: runtime.TemplateName(),
			Version: runtime.Version(), State: cloneRuntimeState(state),
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
	plans := make([]*SettlementPlan, 0)
	for _, key := range s.sortedKeys() {
		runtime := s.runtimes[key]
		if runtime == nil {
			return nil, fmt.Errorf("nil template runtime %s", key)
		}
		runtimeGasConfig := GasConfigForRuntime(gasConfig, runtime)
		plan, err := runtime.SettleBlockWithGasConfigAndPrecision(height, runtimeGasConfig, assetPrecision)
		if err != nil {
			return nil, err
		}
		if settlementPlanHasChanges(plan) {
			plans = append(plans, plan)
		}
	}
	return plans, nil
}

// ReconcileAssetCaches now validates backing quantities without importing an
// address's unsolicited balance into AMM/exchange inventory. Business pools
// change only through accepted operations and their settlement, never by
// observing extra physical UTXOs.
func (s *RuntimeStore) ReconcileAssetCaches(provider ContractUTXOProvider, _ GasConfig) error {
	if s == nil || provider == nil {
		return nil
	}
	for _, key := range s.sortedKeys() {
		runtime := s.runtimes[key]
		if runtime == nil || runtime.base == nil {
			return fmt.Errorf("nil template runtime %s", key)
		}
		view, err := contractframework.CollectResultPlanUTXOs(ResultPlan{Contract: key}, provider)
		if err != nil {
			return err
		}
		physical := contractcommon.ManagedBalance{Value: view.Value, Assets: view.Assets}
		managed := runtime.base.managed
		if err := physical.Debit(managed.Value, managed.Assets); err != nil {
			return fmt.Errorf("%w: %s: %v", contractframework.ErrAccountingInvariant, key, err)
		}
	}
	return nil
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
	return total, err == nil
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
	if s != nil {
		for _, key := range s.sortedKeys() {
			writeLengthPrefixed(h, []byte(key))
			if s.runtimes[key] == nil {
				return [32]byte{}
			}
			root := s.runtimes[key].StateRoot()
			writeLengthPrefixed(h, root[:])
		}
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
