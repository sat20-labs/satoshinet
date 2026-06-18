package agent

import (
	"encoding/json"
	"fmt"
)

func (s *RuntimeStore) Clone() *RuntimeStore {
	encoded, err := s.MarshalBinary()
	if err != nil {
		return NewRuntimeStore()
	}
	clone, err := DecodeRuntimeStore(encoded)
	if err != nil {
		return NewRuntimeStore()
	}
	return clone
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
		Type:            ContractTypeAgent,
		SubType:         snapshot.Subtype,
		Version:         snapshot.Deploy.AgentVersion,
		GasLimit:        snapshot.Deploy.GasLimit,
		DeployNonce:     snapshot.Deploy.DeployNonce,
		ContractContent: content,
	}
	if deploy.Version == 0 {
		deploy.Version = snapshot.Version
	}
	runtime, err := NewRuntimeWithDeployer(addr, deploy, snapshot.Config, snapshot.Deploy.Deployer)
	if err != nil {
		return nil, err
	}
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
