package dkvs

import (
	"math/big"
	"strings"
	"sync"
)

// HeightCachedAutopayStateProvider reuses contract state within one indexed
// block. Contract state only changes when a new block is applied.
type HeightCachedAutopayStateProvider struct {
	Provider      AutopayStateProvider
	CurrentHeight func() uint64

	mutex  sync.Mutex
	height uint64
	states map[string]*AutopayContractState
}

func (p *HeightCachedAutopayStateProvider) GetAutopayState(contract string) (*AutopayContractState, error) {
	if p == nil || p.Provider == nil {
		return nil, ErrInvalidFeeProof
	}
	height := uint64(0)
	if p.CurrentHeight != nil {
		height = p.CurrentHeight()
	}
	if height == 0 {
		state, err := p.Provider.GetAutopayState(contract)
		if err != nil {
			return nil, err
		}
		return normalizeAutopayStateForPaidRetention(state), nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.height != height || p.states == nil {
		p.height = height
		p.states = make(map[string]*AutopayContractState)
	}
	if state, ok := p.states[contract]; ok {
		return cloneAutopayState(state), nil
	}
	state, err := p.Provider.GetAutopayState(contract)
	if err != nil {
		return nil, err
	}
	state = normalizeAutopayStateForPaidRetention(state)
	p.states[contract] = cloneAutopayState(state)
	return cloneAutopayState(state), nil
}

// normalizeAutopayStateForPaidRetention converts the contract's per-block
// payment history into the active/funding shape consumed by the existing DKVS
// fee verifier. A delegate is active only after it has paid the current block.
// The synthetic balance is verifier-local and never changes contract state.
func normalizeAutopayStateForPaidRetention(state *AutopayContractState) *AutopayContractState {
	state = cloneAutopayState(state)
	if state == nil || state.CurrentBlock <= 0 || state.Closed || strings.EqualFold(state.Status, "closed") ||
		strings.EqualFold(state.Status, "expired") {
		return state
	}
	active := false
	for payer, delegate := range state.Delegates {
		paidCurrent := delegate.LastPayHeight >= state.CurrentBlock
		if !paidCurrent {
			delegate.Status = "funding"
			delegate.Balance = "0"
			state.Delegates[payer] = delegate
			continue
		}
		amount, amountOK := new(big.Rat).SetString(strings.TrimSpace(delegate.AmountPerBlock))
		if !amountOK || amount.Sign() <= 0 {
			delegate.Status = "funding"
			delegate.Balance = "0"
			state.Delegates[payer] = delegate
			continue
		}
		balance, balanceOK := new(big.Rat).SetString(strings.TrimSpace(delegate.Balance))
		if !balanceOK || balance.Sign() < 0 || balance.Cmp(amount) < 0 {
			delegate.Balance = delegate.AmountPerBlock
		}
		delegate.Status = "active"
		state.Delegates[payer] = delegate
		active = true
	}
	if active {
		state.Status = "active"
	} else {
		state.Status = "funding"
	}
	return state
}

func cloneAutopayState(state *AutopayContractState) *AutopayContractState {
	if state == nil {
		return nil
	}
	copyState := *state
	if state.Delegates != nil {
		copyState.Delegates = make(map[string]AutopayDelegateState, len(state.Delegates))
		for key, delegate := range state.Delegates {
			copyState.Delegates[key] = delegate
		}
	}
	return &copyState
}
