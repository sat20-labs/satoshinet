package template

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

type ContractRuntime struct {
	base     *RuntimeBase
	contract Contract
}

func NewRuntime(address ContractAddress, deploy DeployPayload, registry *Registry) (*ContractRuntime, error) {
	base, err := NewRuntimeBase(address, deploy)
	if err != nil {
		return nil, err
	}
	if registry == nil {
		registry = NewDefaultRegistry()
	}

	contract, err := registry.NewContract(deploy.TemplateName)
	if err != nil {
		return nil, err
	}
	if err := contract.Decode(deploy.ContractContent); err != nil {
		return nil, fmt.Errorf("decode template contract content: %w", err)
	}
	if contract.TemplateName() != deploy.TemplateName {
		return nil, fmt.Errorf("template name mismatch %s != %s", contract.TemplateName(), deploy.TemplateName)
	}
	if contract.Version() != deploy.TemplateVersion {
		return nil, fmt.Errorf("template version mismatch %d != %d", contract.Version(), deploy.TemplateVersion)
	}
	if err := contract.CheckContent(); err != nil {
		return nil, err
	}

	runtime := &ContractRuntime{
		base:     base,
		contract: contract,
	}
	if err := runtime.initializeRuntimeState(); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *ContractRuntime) Address() ContractAddress {
	return r.base.Address()
}

func (r *ContractRuntime) URL() string {
	return r.base.URL()
}

func (r *ContractRuntime) TemplateName() string {
	return r.contract.TemplateName()
}

func (r *ContractRuntime) Version() uint32 {
	return r.contract.Version()
}

func (r *ContractRuntime) Encode() ([]byte, error) {
	return r.contract.Encode()
}

func (r *ContractRuntime) Decode(data []byte) error {
	return r.contract.Decode(data)
}

func (r *ContractRuntime) CheckContent() error {
	return r.contract.CheckContent()
}

func (r *ContractRuntime) Contract() Contract {
	return r.contract
}

func (r *ContractRuntime) RuntimeBase() *RuntimeBase {
	return r.base
}

func (r *ContractRuntime) CheckInvoke(action string, param []byte) error {
	invokable, ok := r.contract.(InvokableContract)
	if !ok {
		return fmt.Errorf("template %s does not support invoke", r.TemplateName())
	}
	return invokable.CheckInvoke(action, param)
}

func (r *ContractRuntime) ApplyInvoke(req ApplyInvokeRequest) (*InvokeItem, error) {
	if err := r.CheckInvoke(req.Action, req.Param); err != nil {
		return nil, err
	}
	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	item, err := NewInvokeItemFromRequest(r.contract, state.NextItemID, req)
	if err != nil {
		return nil, err
	}
	state.NextItemID++
	state.InvokeCount++
	state.Items = append(state.Items, *item)
	state.Running.Apply(item)
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *ContractRuntime) SettleBlock(height int64) (*SettlementPlan, error) {
	return r.SettleBlockWithGasConfig(height, DefaultGasConfig())
}

func (r *ContractRuntime) SettleBlockWithGasConfig(height int64, gasConfig GasConfig) (*SettlementPlan, error) {
	switch r.contract.(type) {
	case *LimitOrderContract:
		return r.settleLimitOrders(height)
	case *AMMContract:
		return r.settleAMM(height)
	case *ExchangeContract:
		return r.settleExchange(height, gasConfig.normalized())
	default:
		addr := r.Address()
		return &SettlementPlan{
			Contract: addr.EncodeAddress(),
			Height:   height,
		}, nil
	}
}

func (r *ContractRuntime) StateRoot() [32]byte {
	return r.base.StateRoot()
}

func (r *ContractRuntime) SetCurrentBlock(height int64) {
	r.base.SetCurrentBlock(height)
}

func (r *ContractRuntime) CurrentBlock() int64 {
	return r.base.CurrentBlock()
}

func (r *ContractRuntime) InvokeCount() uint64 {
	return r.base.InvokeCount()
}

func (r *ContractRuntime) IncrementInvokeCount() {
	r.base.IncrementInvokeCount()
}

func (r *ContractRuntime) SetState(key string, value []byte) {
	r.base.SetState(key, value)
}

func (r *ContractRuntime) GetState(key string) ([]byte, bool) {
	return r.base.GetState(key)
}

type RuntimeBase struct {
	address         ContractAddress
	templateName    string
	templateVersion uint32
	deployer        string
	random          []byte
	contractContent []byte
	currentBlock    int64
	invokeCount     uint64
	state           map[string][]byte
}

func NewRuntimeBase(address ContractAddress, deploy DeployPayload) (*RuntimeBase, error) {
	if deploy.TemplateName == "" {
		return nil, errors.New("template name is empty")
	}
	if deploy.TemplateVersion == 0 {
		return nil, errors.New("template version is zero")
	}
	if deploy.Deployer == "" {
		return nil, errors.New("deployer is empty")
	}
	if len(deploy.Random) == 0 {
		return nil, errors.New("random value is empty")
	}
	if len(deploy.ContractContent) == 0 {
		return nil, errors.New("contract content is empty")
	}

	return &RuntimeBase{
		address:         address,
		templateName:    deploy.TemplateName,
		templateVersion: deploy.TemplateVersion,
		deployer:        deploy.Deployer,
		random:          append([]byte(nil), deploy.Random...),
		contractContent: append([]byte(nil), deploy.ContractContent...),
		state:           make(map[string][]byte),
	}, nil
}

func (r *RuntimeBase) Address() ContractAddress {
	return r.address
}

func (r *RuntimeBase) URL() string {
	return r.address.EncodeAddress()
}

func (r *RuntimeBase) TemplateName() string {
	return r.templateName
}

func (r *RuntimeBase) Version() uint32 {
	return r.templateVersion
}

func (r *RuntimeBase) Deployer() string {
	return r.deployer
}

func (r *RuntimeBase) CurrentBlock() int64 {
	return r.currentBlock
}

func (r *RuntimeBase) SetCurrentBlock(height int64) {
	r.currentBlock = height
}

func (r *RuntimeBase) InvokeCount() uint64 {
	return r.invokeCount
}

func (r *RuntimeBase) IncrementInvokeCount() {
	r.invokeCount++
}

func (r *RuntimeBase) SetState(key string, value []byte) {
	if r.state == nil {
		r.state = make(map[string][]byte)
	}
	r.state[key] = append([]byte(nil), value...)
}

func (r *RuntimeBase) GetState(key string) ([]byte, bool) {
	value, ok := r.state[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), value...), true
}

func (r *RuntimeBase) StateRoot() [32]byte {
	h := sha256.New()
	writeLengthPrefixed(h, []byte(r.address.EncodeAddress()))
	writeLengthPrefixed(h, []byte(r.templateName))
	writeUint32(h, r.templateVersion)
	writeLengthPrefixed(h, []byte(r.deployer))
	writeLengthPrefixed(h, r.random)
	writeLengthPrefixed(h, r.contractContent)
	writeUint64(h, uint64(r.currentBlock))
	writeUint64(h, r.invokeCount)

	keys := make([]string, 0, len(r.state))
	for key := range r.state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeLengthPrefixed(h, []byte(key))
		writeLengthPrefixed(h, r.state[key])
	}

	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

type byteWriter interface {
	Write([]byte) (int, error)
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
