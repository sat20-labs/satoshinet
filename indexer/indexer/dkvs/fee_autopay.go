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
	Contract      string `json:"contract,omitempty"`
	TemplateName  string `json:"templateName"`
	Deployer      string `json:"deployer,omitempty"`
	CurrentBlock  int64  `json:"currentBlock,omitempty"`
	Recipient     string `json:"recipient"`
	FeeAssetName  string `json:"feeAssetName"`
	ScheduleMode  string `json:"scheduleMode"`
	BaseAmount    string `json:"baseAmount"`
	StepAmount    string `json:"stepAmount,omitempty"`
	EndHeight     int64  `json:"endHeight,omitempty"`
	Status        string `json:"status"`
	FeeBalance    string `json:"feeBalance,omitempty"`
	GasBalance    string `json:"gasBalance,omitempty"`
	ActiveHeight  int64  `json:"activeHeight,omitempty"`
	NextPayHeight int64  `json:"nextPayHeight,omitempty"`
	LastPayHeight int64  `json:"lastPayHeight,omitempty"`
	PaidBlocks    int64  `json:"paidBlocks,omitempty"`
	Closed        bool   `json:"closed,omitempty"`
}

type AutopayFeeVerifier struct {
	StateProvider         AutopayStateProvider
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
		if strings.TrimSpace(candidateProof.PoolContract) == capacity.Contract {
			count++
			if count > capacity.MaxRecords {
				return ErrFeeCapacityExceeded
			}
		}
	}
	_ = existing
	return nil
}

type autopayCapacity struct {
	Contract   string
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
	maxRecords, err := v.maxRecordsForState(state)
	if err != nil {
		return proof, capacity, err
	}
	capacity = autopayCapacity{
		Contract:   strings.TrimSpace(proof.PoolContract),
		MaxRecords: maxRecords,
	}
	return proof, capacity, nil
}

func (v AutopayFeeVerifier) verifyState(proof *FeeProof, payer string, expiryHeight uint64) (*AutopayContractState, error) {
	if v.StateProvider == nil {
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
	if !strings.EqualFold(strings.TrimSpace(state.Deployer), strings.TrimSpace(payer)) {
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
	if state.EndHeight > 0 && expiryHeight > uint64(state.EndHeight) {
		return state, ErrInvalidFeeProof
	}
	return state, nil
}

func (v AutopayFeeVerifier) maxRecordsForState(state *AutopayContractState) (uint64, error) {
	if state == nil {
		return 0, ErrInvalidFeeProof
	}
	amount, err := autopayAmountPerBlock(state)
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

func autopayAmountPerBlock(state *AutopayContractState) (*big.Rat, error) {
	base, err := positiveRat(state.BaseAmount)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(state.ScheduleMode) != "linear" {
		return base, nil
	}
	step, err := nonNegativeRat(state.StepAmount)
	if err != nil {
		return nil, err
	}
	offset := state.CurrentBlock - (state.ActiveHeight + 1)
	if offset < 0 {
		offset = 0
	}
	if offset == 0 || step.Sign() == 0 {
		return base, nil
	}
	return base.Add(base, new(big.Rat).Mul(step, new(big.Rat).SetInt64(offset))), nil
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
