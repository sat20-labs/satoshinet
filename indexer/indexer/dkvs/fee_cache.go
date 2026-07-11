package dkvs

import "sync"

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
		return p.Provider.GetAutopayState(contract)
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
	p.states[contract] = cloneAutopayState(state)
	return cloneAutopayState(state), nil
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
