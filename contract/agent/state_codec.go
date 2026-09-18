package agent

import (
	"encoding/json"
	"fmt"
)

// Missing managed quantities cannot be reconstructed from a physical address
// balance: that would silently accept unsolicited funds as user backing.
func (s *RuntimeSnapshot) UnmarshalJSON(data []byte) error {
	type snapshot RuntimeSnapshot
	var decoded snapshot
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	managed, present := fields["managed"]
	if !present || string(managed) == "null" {
		return fmt.Errorf("agent snapshot has no managed balance; rebuild the contract state")
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = RuntimeSnapshot(decoded)
	return nil
}

// Clone retains local capabilities while copying all mutable execution state.
func (s *RuntimeStore) Clone() *RuntimeStore {
	out := NewRuntimeStore()
	if s == nil {
		return out
	}
	for key, runtime := range s.runtimes {
		out.runtimes[key] = runtime.Clone()
	}
	return out
}

func (s *RuntimeStore) MarshalBinary() ([]byte, error) {
	return json.Marshal(s)
}

func DecodeRuntimeStore(data []byte) (*RuntimeStore, error) {
	if len(data) == 0 {
		return NewRuntimeStore(), nil
	}
	var snapshots []RuntimeSnapshot
	if err := json.Unmarshal(data, &snapshots); err != nil {
		return nil, err
	}
	store := NewRuntimeStore()
	for _, snapshot := range snapshots {
		runtime, err := runtimeFromSnapshot(snapshot)
		if err != nil {
			return nil, err
		}
		if store.Exists(runtime.Address()) {
			return nil, fmt.Errorf("duplicate agent runtime %s", snapshot.Address)
		}
		store.Add(runtime)
	}
	return store, nil
}

func runtimeFromSnapshot(snapshot RuntimeSnapshot) (*Runtime, error) {
	addr, err := DecodeContractAddress(snapshot.Address)
	if err != nil {
		return nil, err
	}
	content, err := snapshot.Contract.Encode()
	if err != nil {
		return nil, err
	}
	deploy := DeployPayload{
		Type: ContractTypeAgent, SubType: snapshot.Subtype, Version: snapshot.Deploy.AgentVersion,
		GasLimit: snapshot.Deploy.GasLimit, DeployNonce: snapshot.Deploy.DeployNonce,
		Flags: snapshot.Deploy.Flags, ContractContent: content,
	}
	if deploy.Version == 0 {
		deploy.Version = snapshot.Version
	}
	runtime, err := NewRuntimeWithDeployer(addr, deploy, snapshot.Config, snapshot.Deploy.Deployer)
	if err != nil {
		return nil, err
	}
	if err := snapshot.Managed.Validate(); err != nil {
		return nil, fmt.Errorf("invalid managed agent balance: %w", err)
	}
	runtime.managed = snapshot.Managed.Clone()
	stateJSON, err := json.Marshal(snapshot.State)
	if err != nil {
		return nil, err
	}
	if err := runtime.LoadStateJSON(stateJSON); err != nil {
		return nil, err
	}
	runtimeAddr := runtime.Address()
	if runtimeAddr.EncodeAddress() != snapshot.Address {
		return nil, fmt.Errorf("agent runtime snapshot address mismatch")
	}
	return runtime, nil
}
