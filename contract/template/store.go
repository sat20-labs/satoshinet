package template

import (
	"crypto/sha256"
	"sort"
)

type RuntimeStore struct {
	runtimes map[string]*ContractRuntime
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

func (s *RuntimeStore) SettleBlock(height int64) ([]*SettlementPlan, error) {
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
		plan, err := s.runtimes[key].SettleBlock(height)
		if err != nil {
			return nil, err
		}
		if settlementPlanHasChanges(plan) {
			plans = append(plans, plan)
		}
	}
	return plans, nil
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
