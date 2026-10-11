package template

import (
	"crypto/sha256"
	"sort"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

func (r *ContractRuntime) StateRoot() [32]byte {
	h := sha256.New()
	writeLengthPrefixed(h, []byte(r.base.address.EncodeAddress()))
	writeLengthPrefixed(h, []byte(r.base.templateName))
	writeUint32(h, r.base.templateVersion)
	writeLengthPrefixed(h, []byte(r.base.deployer))
	writeUint64(h, r.base.deployNonce)
	writeUint32(h, uint32(r.base.flags))
	writeLengthPrefixed(h, r.base.contractContent)
	e := contractcommon.NewStateEncoderTo(h)
	contractcommon.WriteManagedBalance(e, r.base.managed)
	if e.Err != nil {
		return [32]byte{}
	}
	writeUint64(h, uint64(r.base.currentBlock))
	writeUint64(h, r.base.invokeCount)

	state, err := r.loadRuntimeState()
	if err != nil {
		keys := make([]string, 0, len(r.base.state))
		for key := range r.base.state {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := r.base.state[key]
			writeLengthPrefixed(h, []byte(key))
			writeLengthPrefixed(h, value)
		}
	} else {
		writeLengthPrefixed(h, []byte(runtimeStateKey))
		writeTemplateRootState(e, r.contract, state)
		if e.Err != nil {
			return [32]byte{}
		}
	}

	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

// Keep the same logical projection as execution: only unfinished items and
// the deployed template's running data. No projected struct or item slice.
func writeTemplateRootState(e *contractcommon.StateEncoder, contract Contract, state TemplateRuntimeState) {
	e.I64(state.NextItemID)
	e.U64(state.InvokeCount)
	count := uint64(0)
	for i := range state.Items {
		if !state.Items[i].Finished() {
			count++
		}
	}
	e.U64(count)
	for i := range state.Items {
		if !state.Items[i].Finished() {
			writeCompactInvokeItemFields(e, state.Items[i])
		}
	}
	switch contract.(type) {
	case *LimitOrderContract:
		e.Bool(state.LimitOrder != nil)
		if state.LimitOrder != nil {
			writeCompactLimitOrderRunningData(e, *state.LimitOrder)
		}
	case *AMMContract:
		e.Bool(state.AMM != nil)
		if state.AMM != nil {
			writeCompactAMMRunningData(e, *state.AMM)
		}
	case *ExchangeContract:
		e.Bool(state.Exchange != nil)
		if state.Exchange != nil {
			writeCompactExchangeRunningData(e, *state.Exchange)
		}
	case *AutopayContract:
		e.Bool(state.Autopay != nil)
		if state.Autopay != nil {
			writeCompactAutopayRunningData(e, *state.Autopay)
		}
	}
}

func (s *RuntimeStore) PruneFinishedItems() error {
	if s == nil {
		return nil
	}
	for _, runtime := range s.runtimes {
		if runtime == nil {
			continue
		}
		state, err := runtime.loadRuntimeState()
		if err != nil {
			return err
		}
		kept := 0
		for i := range state.Items {
			if !state.Items[i].Finished() {
				state.Items[kept] = state.Items[i]
				kept++
			}
		}
		if kept == len(state.Items) {
			continue
		}
		clear(state.Items[kept:])
		state.Items = state.Items[:kept]
		if err := runtime.saveRuntimeState(state); err != nil {
			return err
		}
	}
	return nil
}
