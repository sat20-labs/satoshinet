package template

import (
	"encoding/json"
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

type runtimeStoreSnapshot struct {
	Runtimes []runtimeSnapshot `json:"runtimes"`
}

type runtimeSnapshot struct {
	Address         string            `json:"address"`
	TemplateName    string            `json:"templateName"`
	TemplateVersion uint32            `json:"templateVersion"`
	Deployer        string            `json:"deployer"`
	Random          []byte            `json:"random"`
	ContractContent []byte            `json:"contractContent"`
	CurrentBlock    int64             `json:"currentBlock"`
	InvokeCount     uint64            `json:"invokeCount"`
	State           map[string][]byte `json:"state"`
}

func (s *RuntimeStore) MarshalBinary() ([]byte, error) {
	snapshot := runtimeStoreSnapshot{}
	if s != nil {
		keys := s.sortedKeys()
		snapshot.Runtimes = make([]runtimeSnapshot, 0, len(keys))
		for _, key := range keys {
			runtime := s.runtimes[key]
			snapshot.Runtimes = append(snapshot.Runtimes, snapshotRuntime(runtime))
		}
	}
	return json.Marshal(snapshot)
}

func DecodeRuntimeStore(data []byte, registry *Registry) (*RuntimeStore, error) {
	if len(data) == 0 {
		return NewRuntimeStore(), nil
	}
	var snapshot runtimeStoreSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	store := NewRuntimeStore()
	for _, item := range snapshot.Runtimes {
		runtime, err := restoreRuntime(item, registry)
		if err != nil {
			return nil, err
		}
		store.Add(runtime)
	}
	return store, nil
}

func snapshotRuntime(runtime *ContractRuntime) runtimeSnapshot {
	base := runtime.base
	state := make(map[string][]byte, len(base.state))
	for key, value := range base.state {
		state[key] = append([]byte(nil), value...)
	}
	return runtimeSnapshot{
		Address:         base.address.EncodeAddress(),
		TemplateName:    base.templateName,
		TemplateVersion: base.templateVersion,
		Deployer:        base.deployer,
		Random:          append([]byte(nil), base.random...),
		ContractContent: append([]byte(nil), base.contractContent...),
		CurrentBlock:    base.currentBlock,
		InvokeCount:     base.invokeCount,
		State:           state,
	}
}

func restoreRuntime(snapshot runtimeSnapshot, registry *Registry) (*ContractRuntime, error) {
	address, err := contractcommon.DecodeContractAddress(snapshot.Address)
	if err != nil {
		return nil, err
	}
	deploy := DeployPayload{
		GasLimit:        1,
		TemplateName:    snapshot.TemplateName,
		TemplateVersion: snapshot.TemplateVersion,
		Deployer:        snapshot.Deployer,
		Random:          snapshot.Random,
		ContractContent: snapshot.ContractContent,
	}
	runtime, err := NewRuntime(address, deploy, registry)
	if err != nil {
		return nil, err
	}
	runtimeAddress := runtime.Address()
	if runtimeAddress.EncodeAddress() != address.EncodeAddress() {
		return nil, fmt.Errorf("restored template runtime address mismatch")
	}
	runtime.base.currentBlock = snapshot.CurrentBlock
	runtime.base.invokeCount = snapshot.InvokeCount
	runtime.base.state = make(map[string][]byte, len(snapshot.State))
	for key, value := range snapshot.State {
		runtime.base.state[key] = append([]byte(nil), value...)
	}
	return runtime, nil
}
