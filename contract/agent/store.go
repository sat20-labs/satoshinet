package agent

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
)

type RuntimeStore struct {
	runtimes map[string]*Runtime
}

type RuntimeSnapshot struct {
	Address  string              `json:"address"`
	Subtype  string              `json:"subtype"`
	Version  uint32              `json:"version"`
	Contract PredictionContract  `json:"contract"`
	State    RuntimeState        `json:"state"`
	Config   RuntimeConfig       `json:"config"`
	Deploy   DeployPayloadHeader `json:"deploy"`
}

type DeployPayloadHeader struct {
	GasLimit     uint64 `json:"gas_limit"`
	Deployer     string `json:"deployer"`
	Random       []byte `json:"random"`
	ContentHash  []byte `json:"content_hash"`
	AgentVersion uint32 `json:"agent_version"`
}

func NewRuntimeStore() *RuntimeStore {
	return &RuntimeStore{runtimes: make(map[string]*Runtime)}
}

func (s *RuntimeStore) Add(runtime *Runtime) {
	if s.runtimes == nil {
		s.runtimes = make(map[string]*Runtime)
	}
	address := runtime.Address()
	s.runtimes[address.EncodeAddress()] = runtime
}

func (s *RuntimeStore) Get(contract ContractAddress) (*Runtime, bool) {
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

func (s *RuntimeStore) StateRoot() [32]byte {
	if s == nil {
		h := sha256.New()
		var root [32]byte
		copy(root[:], h.Sum(nil))
		return root
	}
	return stateRootFromRuntimes(s.runtimes)
}

func (s *RuntimeStore) Snapshots() ([]RuntimeSnapshot, error) {
	keys := s.sortedKeys()
	out := make([]RuntimeSnapshot, 0, len(keys))
	for _, key := range keys {
		runtime := s.runtimes[key]
		if runtime == nil {
			continue
		}
		contentHash := sha256.Sum256(runtime.deploy.ContractContent)
		out = append(out, RuntimeSnapshot{
			Address:  runtimeAddressString(runtime),
			Subtype:  runtime.deploy.Subtype,
			Version:  runtime.deploy.AgentVersion,
			Contract: runtime.Contract(),
			State:    runtime.State(),
			Config:   runtime.config,
			Deploy: DeployPayloadHeader{
				GasLimit:     runtime.deploy.GasLimit,
				Deployer:     runtime.deploy.Deployer,
				Random:       append([]byte(nil), runtime.deploy.Random...),
				ContentHash:  contentHash[:],
				AgentVersion: runtime.deploy.AgentVersion,
			},
		})
	}
	return out, nil
}

func runtimeAddressString(runtime *Runtime) string {
	address := runtime.Address()
	return address.EncodeAddress()
}

func (s *RuntimeStore) MarshalJSON() ([]byte, error) {
	snapshots, err := s.Snapshots()
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshots)
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
