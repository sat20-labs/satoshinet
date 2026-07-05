package agent

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
)

type byteWriter interface {
	Write([]byte) (int, error)
}

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
	GasLimit     int64  `json:"gas_limit"`
	Deployer     string `json:"deployer"`
	DeployNonce  uint64 `json:"deploy_nonce"`
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

func (s *RuntimeStore) ActiveNetworkExclusiveExists(runtime *Runtime) bool {
	if s == nil || runtime == nil || !runtime.Contract().NetworkExclusive() {
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

func (r *Runtime) NetworkExclusiveKey() string {
	if r == nil {
		return ""
	}
	sum := sha256.Sum256(r.deploy.ContractContent)
	return r.deploy.SubType + ":" + strconv.FormatUint(uint64(r.deploy.Version), 10) + ":" +
		hex.EncodeToString(sum[:])
}

func (r *Runtime) NetworkExclusiveActive() bool {
	if r == nil {
		return false
	}
	status := r.state.Status
	return status != StatusCompleted && status != StatusRejected
}

func (r *Runtime) StateRoot() [32]byte {
	h := sha256.New()
	writeLengthPrefixed(h, []byte(r.address.EncodeAddress()))
	writeLengthPrefixed(h, []byte(r.deploy.SubType))
	writeUint32(h, r.deploy.Version)
	writeLengthPrefixed(h, []byte(r.deployer))
	writeUint64(h, r.deploy.DeployNonce)
	writeLengthPrefixed(h, r.deploy.ContractContent)
	stateJSON, _ := r.StateJSON()
	writeLengthPrefixed(h, stateJSON)
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

func writeLengthPrefixed(buf byteWriter, data []byte) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(data)))
	buf.Write(lenBuf[:])
	buf.Write(data)
}

func writeUint32(buf byteWriter, v uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	buf.Write(tmp[:])
}

func writeUint64(buf byteWriter, v uint64) {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	buf.Write(tmp[:])
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

func stateRootFromRuntimes(runtimes map[string]*Runtime) [32]byte {
	h := sha256.New()
	keys := make([]string, 0, len(runtimes))
	for key := range runtimes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeLengthPrefixed(h, []byte(key))
		root := runtimes[key].StateRoot()
		writeLengthPrefixed(h, root[:])
	}
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

func (s *RuntimeStore) ApplyConfig(cfg RuntimeConfig) {
	if s == nil {
		return
	}
	for _, runtime := range s.runtimes {
		if runtime != nil {
			runtime.config = cfg
		}
	}
}

func (s *RuntimeStore) AdvancePredictionStatuses(heightValue, unixValue int64) bool {
	changed := false
	for _, key := range s.sortedKeys() {
		runtime := s.runtimes[key]
		if runtime == nil {
			continue
		}
		timeValue := heightValue
		if runtime.contract.TimeBase == TimeBaseUnix && unixValue != 0 {
			timeValue = unixValue
		}
		if runtime.AdvancePredictionStatus(timeValue) {
			changed = true
		}
	}
	return changed
}

func (s *RuntimeStore) HasDuePredictionStatusAdvance(heightValue, unixValue int64) bool {
	if s == nil {
		return false
	}
	return s.Clone().AdvancePredictionStatuses(heightValue, unixValue)
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
			Subtype:  runtime.deploy.SubType,
			Version:  runtime.deploy.Version,
			Contract: runtime.Contract(),
			State:    runtime.State(),
			Config:   runtime.config,
			Deploy: DeployPayloadHeader{
				GasLimit:     runtime.deploy.GasLimit,
				Deployer:     runtime.deployer,
				DeployNonce:  runtime.deploy.DeployNonce,
				ContentHash:  contentHash[:],
				AgentVersion: runtime.deploy.Version,
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
