package dkvs

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	autopayTemplateName = "autopay.tc"
	autopayStatusActive = "active"
)

type AutopayStateProvider interface {
	GetAutopayState(contract string) (*AutopayContractState, error)
}

type ContractStateCall func(method string, params []interface{}) (json.RawMessage, error)

type RPCAutopayStateProvider struct {
	Call ContractStateCall
}

type AutopayContractState struct {
	Contract          string                          `json:"contract,omitempty"`
	TemplateName      string                          `json:"templateName"`
	Deployer          string                          `json:"deployer,omitempty"`
	CurrentBlock      int64                           `json:"currentBlock,omitempty"`
	ServiceName       string                          `json:"serviceName,omitempty"`
	Recipient         string                          `json:"recipient"`
	FeeAssetName      string                          `json:"feeAssetName"`
	MinAmountPerBlock string                          `json:"minAmountPerBlock"`
	Status            string                          `json:"status"`
	FeeBalance        string                          `json:"feeBalance,omitempty"`
	GasBalance        string                          `json:"gasBalance,omitempty"`
	ActiveHeight      int64                           `json:"activeHeight,omitempty"`
	NextPayHeight     int64                           `json:"nextPayHeight,omitempty"`
	LastPayHeight     int64                           `json:"lastPayHeight,omitempty"`
	PaidBlocks        int64                           `json:"paidBlocks,omitempty"`
	Closed            bool                            `json:"closed,omitempty"`
	Delegates         map[string]AutopayDelegateState `json:"delegates,omitempty"`
}

type AutopayDelegateState struct {
	AmountPerBlock string `json:"amountPerBlock,omitempty"`
	Balance        string `json:"balance,omitempty"`
	TotalPaid      string `json:"totalPaid,omitempty"`
	PaidBlockCount int64  `json:"paidBlockCount,omitempty"`
	LastPayHeight  int64  `json:"lastPayHeight,omitempty"`
	Status         string `json:"status,omitempty"`
}

type AutopayFeeVerifier struct {
	StateProvider         AutopayStateProvider
	Contract              string
	ServiceName           string
	Recipient             string
	FeeAssetName          string
	FullRecordFeePerBlock string
	AddressParams         *chaincfg.Params
}

func (p RPCAutopayStateProvider) GetAutopayState(contract string) (*AutopayContractState, error) {
	if p.Call == nil {
		return nil, ErrInvalidFeeProof
	}
	raw, err := p.Call("getcontractstate", []interface{}{strings.TrimSpace(contract)})
	if err != nil {
		return nil, err
	}
	return DecodeAutopayContractState(raw, contract)
}

func DecodeAutopayContractState(raw []byte, contract string) (*AutopayContractState, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidFeeProof
	}
	var wrapper struct {
		Code    int                    `json:"code,omitempty"`
		Msg     string                 `json:"msg,omitempty"`
		Data    json.RawMessage        `json:"data,omitempty"`
		State   json.RawMessage        `json:"state,omitempty"`
		Details map[string]interface{} `json:"details,omitempty"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, err
	}
	if wrapper.Code != 0 {
		if wrapper.Msg != "" {
			return nil, fmt.Errorf("%s", wrapper.Msg)
		}
		return nil, ErrInvalidFeeProof
	}
	if existsValue, ok := wrapper.Details["exists"]; ok {
		if exists, ok := existsValue.(bool); ok && !exists {
			return nil, ErrInvalidFeeProof
		}
	}
	src := raw
	switch {
	case len(wrapper.Data) != 0 && string(wrapper.Data) != "null":
		return DecodeAutopayContractState(wrapper.Data, contract)
	case len(wrapper.State) != 0 && string(wrapper.State) != "null":
		src = wrapper.State
	}
	var state AutopayContractState
	if err := json.Unmarshal(src, &state); err != nil {
		return nil, err
	}
	if state.Contract == "" {
		state.Contract = strings.TrimSpace(contract)
	}
	if state.TemplateName == "" || state.Status == "" {
		return nil, ErrInvalidFeeProof
	}
	return &state, nil
}

func (v AutopayFeeVerifier) VerifyRecordFeeProof(record *wire.DKVSRecord, parsed ParsedKey) error {
	if record == nil {
		return ErrInvalidRecord
	}
	_, _, err := v.verifyProofForRecord(record, parsed)
	return err
}

func (v AutopayFeeVerifier) VerifyFeeProof(recordHash, keyHash [32]byte, namespace string, recordSize int, expiryHeight uint64, feeProof []byte) error {
	_ = recordHash
	_ = keyHash
	_ = namespace
	_ = expiryHeight
	if recordSize < 0 {
		return ErrInvalidFeeProof
	}
	proof, err := ParseFeeProof(feeProof)
	if err != nil {
		return err
	}
	if proof.Mode != FeeModeAutopay || strings.TrimSpace(proof.PoolContract) == "" {
		return ErrInvalidFeeProof
	}
	return nil
}

func (v AutopayFeeVerifier) VerifyFeeCapacity(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, records []*wire.DKVSRecord, height, now uint64) error {
	if record == nil {
		return ErrInvalidRecord
	}
	proof, capacity, err := v.verifyProofForRecord(record, parsed)
	if err != nil {
		return err
	}
	if proof == nil || proof.Mode != FeeModeAutopay {
		return nil
	}
	if capacity.MaxRecords == 0 {
		return ErrFeeCapacityExceeded
	}
	count := uint64(1)
	for _, candidate := range records {
		if candidate == nil || candidate.Key == record.Key || IsExpired(candidate, height, now) {
			continue
		}
		candidateProof, err := ParseFeeProof(candidate.FeeProof)
		if err != nil || candidateProof.Mode != FeeModeAutopay {
			continue
		}
		if strings.TrimSpace(candidateProof.PoolContract) != capacity.Contract {
			continue
		}
		candidatePayer, err := P2TRAddressFromPubKeyBytes(candidate.PubKey, v.AddressParams)
		if err == nil && strings.EqualFold(strings.TrimSpace(candidatePayer), capacity.Payer) {
			count++
			if count > capacity.MaxRecords {
				return ErrFeeCapacityExceeded
			}
		}
	}
	_ = existing
	return nil
}

func (v AutopayFeeVerifier) FeeCapacity(record *wire.DKVSRecord, parsed ParsedKey) (FeeCapacityDescriptor, error) {
	_, capacity, err := v.verifyProofForRecord(record, parsed)
	if err != nil {
		return FeeCapacityDescriptor{}, err
	}
	return FeeCapacityDescriptor{
		UsageKey:   capacity.Contract + "\x00" + capacity.Payer,
		MaxRecords: capacity.MaxRecords,
	}, nil
}

func (v AutopayFeeVerifier) FeeUsageKey(record *wire.DKVSRecord) (string, error) {
	if record == nil {
		return "", ErrInvalidRecord
	}
	proof, err := ParseFeeProof(record.FeeProof)
	if err != nil {
		return "", err
	}
	if proof.Mode != FeeModeAutopay {
		return "", nil
	}
	payer, err := P2TRAddressFromPubKeyBytes(record.PubKey, v.AddressParams)
	if err != nil {
		return "", ErrInvalidFeeProof
	}
	return strings.TrimSpace(proof.PoolContract) + "\x00" + strings.TrimSpace(payer), nil
}

type autopayCapacity struct {
	Contract   string
	Payer      string
	MaxRecords uint64
}

func (v AutopayFeeVerifier) verifyProofForRecord(record *wire.DKVSRecord, parsed ParsedKey) (*FeeProof, autopayCapacity, error) {
	var capacity autopayCapacity
	_ = parsed
	if record == nil {
		return nil, capacity, ErrInvalidRecord
	}
	if RecordSize(record) < 0 {
		return nil, capacity, ErrInvalidFeeProof
	}
	proof, err := ParseFeeProof(record.FeeProof)
	if err != nil {
		return proof, capacity, err
	}
	if proof.Mode != FeeModeAutopay || strings.TrimSpace(proof.PoolContract) == "" {
		return proof, capacity, ErrInvalidFeeProof
	}
	payer, err := P2TRAddressFromPubKeyBytes(record.PubKey, v.AddressParams)
	if err != nil {
		return proof, capacity, ErrInvalidFeeProof
	}
	state, err := v.verifyState(proof, payer, record.ExpiryHeight)
	if err != nil {
		return proof, capacity, err
	}
	maxRecords, err := v.maxRecordsForState(state, payer)
	if err != nil {
		return proof, capacity, err
	}
	capacity = autopayCapacity{
		Contract:   strings.TrimSpace(proof.PoolContract),
		Payer:      strings.TrimSpace(payer),
		MaxRecords: maxRecords,
	}
	return proof, capacity, nil
}

func (v AutopayFeeVerifier) verifyState(proof *FeeProof, payer string, expiryHeight uint64) (*AutopayContractState, error) {
	if v.StateProvider == nil {
		return nil, ErrInvalidFeeProof
	}
	if expected := strings.TrimSpace(v.Contract); expected != "" &&
		!strings.EqualFold(strings.TrimSpace(proof.PoolContract), expected) {
		return nil, ErrInvalidFeeProof
	}
	state, err := v.StateProvider.GetAutopayState(proof.PoolContract)
	if err != nil {
		return nil, err
	}
	if state == nil ||
		strings.TrimSpace(state.TemplateName) != autopayTemplateName ||
		!strings.EqualFold(strings.TrimSpace(state.Status), autopayStatusActive) ||
		state.Closed {
		return state, ErrInvalidFeeProof
	}
	if expected := strings.TrimSpace(v.ServiceName); expected != "" &&
		!strings.EqualFold(strings.TrimSpace(state.ServiceName), expected) {
		return state, ErrInvalidFeeProof
	}
	delegate, ok := state.Delegates[strings.TrimSpace(payer)]
	if !ok || !strings.EqualFold(strings.TrimSpace(delegate.Status), autopayStatusActive) {
		return state, ErrInvalidFeeProof
	}
	if expected := strings.TrimSpace(v.Recipient); expected != "" &&
		!strings.EqualFold(strings.TrimSpace(state.Recipient), expected) {
		return state, ErrInvalidFeeProof
	}
	if expected := strings.TrimSpace(v.FeeAssetName); expected != "" &&
		strings.TrimSpace(state.FeeAssetName) != expected {
		return state, ErrInvalidFeeProof
	}
	amount, err := positiveRat(delegate.AmountPerBlock)
	if err != nil {
		return state, err
	}
	balance, err := nonNegativeRat(delegate.Balance)
	if err != nil {
		return state, err
	}
	if balance.Cmp(amount) < 0 {
		return state, ErrInvalidFeeProof
	}
	_ = expiryHeight
	return state, nil
}

func (v AutopayFeeVerifier) maxRecordsForState(state *AutopayContractState, payer string) (uint64, error) {
	if state == nil {
		return 0, ErrInvalidFeeProof
	}
	delegate, ok := state.Delegates[strings.TrimSpace(payer)]
	if !ok {
		return 0, ErrInvalidFeeProof
	}
	amount, err := positiveRat(delegate.AmountPerBlock)
	if err != nil {
		return 0, err
	}
	fullRecordFee, err := positiveRat(v.FullRecordFeePerBlock)
	if err != nil {
		return 0, err
	}
	q := new(big.Rat).Quo(amount, fullRecordFee)
	maxRecords := new(big.Int).Quo(q.Num(), q.Denom())
	if !maxRecords.IsUint64() {
		return ^uint64(0), nil
	}
	return maxRecords.Uint64(), nil
}

func positiveRat(value string) (*big.Rat, error) {
	rat, err := parseRat(value)
	if err != nil {
		return nil, err
	}
	if rat.Sign() <= 0 {
		return nil, ErrInvalidFeeProof
	}
	return rat, nil
}

func nonNegativeRat(value string) (*big.Rat, error) {
	if strings.TrimSpace(value) == "" {
		return new(big.Rat), nil
	}
	rat, err := parseRat(value)
	if err != nil {
		return nil, err
	}
	if rat.Sign() < 0 {
		return nil, ErrInvalidFeeProof
	}
	return rat, nil
}

func parseRat(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, ErrInvalidFeeProof
	}
	rat := new(big.Rat)
	if _, ok := rat.SetString(value); !ok {
		return nil, ErrInvalidFeeProof
	}
	return rat, nil
}
