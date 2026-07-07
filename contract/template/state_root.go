package template

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
)

const templateStateRootVersion = 1

type templateStateRootPayload struct {
	Version    int          `json:"version"`
	NextID     int64        `json:"nextId"`
	Invokes    uint64       `json:"invokes"`
	Items      []InvokeItem `json:"items,omitempty"`
	LimitOrder interface{}  `json:"limitOrder,omitempty"`
	AMM        interface{}  `json:"amm,omitempty"`
	Exchange   interface{}  `json:"exchange,omitempty"`
	Autopay    interface{}  `json:"autopay,omitempty"`
}

func (r *ContractRuntime) StateRoot() [32]byte {
	h := sha256.New()
	writeLengthPrefixed(h, []byte(r.base.address.EncodeAddress()))
	writeLengthPrefixed(h, []byte(r.base.templateName))
	writeUint32(h, r.base.templateVersion)
	writeLengthPrefixed(h, []byte(r.base.deployer))
	writeUint64(h, r.base.deployNonce)
	writeLengthPrefixed(h, r.base.contractContent)
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
		payload := templateCanonicalStatePayload(r.contract, state)
		encoded, err := json.Marshal(payload)
		if err != nil {
			encoded = nil
		}
		writeLengthPrefixed(h, []byte(runtimeStateKey))
		writeLengthPrefixed(h, encoded)
	}

	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

func templateCanonicalStatePayload(contract Contract, state TemplateRuntimeState) templateStateRootPayload {
	payload := templateStateRootPayload{
		Version: templateStateRootVersion,
		NextID:  state.NextItemID,
		Invokes: state.InvokeCount,
		Items:   unfinishedItems(state.Items),
	}
	switch contract.(type) {
	case *LimitOrderContract:
		payload.LimitOrder = state.LimitOrder
	case *AMMContract:
		payload.AMM = state.AMM
	case *ExchangeContract:
		payload.Exchange = state.Exchange
	case *AutopayContract:
		payload.Autopay = state.Autopay
	}
	return payload
}

func unfinishedItems(items []InvokeItem) []InvokeItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]InvokeItem, 0, len(items))
	for _, item := range items {
		if item.Finished() {
			continue
		}
		out = append(out, item)
	}
	return out
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
		pruned := unfinishedItems(state.Items)
		if len(pruned) == len(state.Items) {
			continue
		}
		state.Items = pruned
		if err := runtime.saveRuntimeState(state); err != nil {
			return err
		}
	}
	return nil
}
