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
	Address         string                         `json:"address"`
	TemplateName    string                         `json:"templateName"`
	TemplateVersion uint32                         `json:"templateVersion"`
	Deployer        string                         `json:"deployer"`
	DeployNonce     uint64                         `json:"deployNonce"`
	Flags           ContractFlags                  `json:"flags"`
	ManagedBalance  *contractcommon.ManagedBalance `json:"managedBalance"`
	ContractContent []byte                         `json:"contractContent"`
	CurrentBlock    int64                          `json:"currentBlock"`
	InvokeCount     uint64                         `json:"invokeCount"`
	State           map[string][]byte              `json:"state"`
}

func (s *RuntimeStore) MarshalBinary() ([]byte, error) {
	snapshot := runtimeStoreSnapshot{}
	if s != nil {
		keys := s.sortedKeys()
		snapshot.Runtimes = make([]runtimeSnapshot, 0, len(keys))
		for _, key := range keys {
			runtime := s.runtimes[key]
			if runtime == nil || runtime.base == nil {
				return nil, fmt.Errorf("nil template runtime %s", key)
			}
			if err := runtime.base.flags.Validate(); err != nil {
				return nil, err
			}
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
		if store.Exists(runtime.Address()) {
			return nil, fmt.Errorf("duplicate template runtime %s", item.Address)
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
	managed := base.managed.Clone()
	return runtimeSnapshot{
		Address:         base.address.EncodeAddress(),
		TemplateName:    base.templateName,
		TemplateVersion: base.templateVersion,
		Deployer:        base.deployer,
		DeployNonce:     base.deployNonce,
		Flags:           base.flags,
		ManagedBalance:  &managed,
		ContractContent: append([]byte(nil), base.contractContent...),
		CurrentBlock:    base.currentBlock,
		InvokeCount:     base.invokeCount,
		State:           state,
	}
}

func restoreRuntime(snapshot runtimeSnapshot, registry *Registry) (*ContractRuntime, error) {
	if snapshot.ManagedBalance == nil {
		return nil, fmt.Errorf("template snapshot has no managed balance; rebuild the contract state")
	}
	if err := snapshot.ManagedBalance.Validate(); err != nil {
		return nil, err
	}
	address, err := contractcommon.DecodeContractAddress(snapshot.Address)
	if err != nil {
		return nil, err
	}
	deploy := DeployPayload{
		Type: ContractTypeTemplate, SubType: snapshot.TemplateName,
		Version: snapshot.TemplateVersion, GasLimit: 1, DeployNonce: snapshot.DeployNonce,
		Flags: snapshot.Flags, ContractContent: snapshot.ContractContent,
	}
	runtime, err := NewRuntimeWithDeployer(address, deploy, registry, snapshot.Deployer)
	if err != nil {
		return nil, err
	}
	runtimeAddress := runtime.Address()
	if runtimeAddress.EncodeAddress() != address.EncodeAddress() {
		return nil, fmt.Errorf("restored template runtime address mismatch")
	}
	runtime.base.currentBlock = snapshot.CurrentBlock
	runtime.base.invokeCount = snapshot.InvokeCount
	runtime.base.managed = snapshot.ManagedBalance.Clone()
	runtime.base.state = make(map[string][]byte, len(snapshot.State))
	for key, value := range snapshot.State {
		runtime.base.state[key] = append([]byte(nil), value...)
	}
	if _, err := runtime.loadRuntimeState(); err != nil {
		return nil, fmt.Errorf("validate restored template runtime state: %w", err)
	}
	return runtime, nil
}
