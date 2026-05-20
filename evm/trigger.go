package evm

import (
	"bytes"
	"fmt"
	"sort"
)

type TriggerKind byte

const (
	TriggerAtHeight TriggerKind = iota + 1
)

type Trigger struct {
	ID       string
	Contract ContractAddress
	Kind     TriggerKind
	Height   int64
	GasLimit uint64
	Calldata []byte
}

type BlockEnvironment struct {
	Height int64
}

type TriggerResolutionContext struct {
	Block          BlockContext
	Runtime        *Runtime
	ContractPrefix string
}

func (t Trigger) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("missing trigger id")
	}
	if err := t.Contract.Validate(); err != nil {
		return fmt.Errorf("invalid trigger contract: %w", err)
	}
	switch t.Kind {
	case TriggerAtHeight:
		if t.Height < 0 {
			return fmt.Errorf("invalid trigger height")
		}
	default:
		return fmt.Errorf("unknown trigger kind %d", t.Kind)
	}
	return nil
}

func (t Trigger) Due(env BlockEnvironment) bool {
	switch t.Kind {
	case TriggerAtHeight:
		return env.Height >= t.Height
	default:
		return false
	}
}

func (t Trigger) Clone() Trigger {
	out := t
	out.Calldata = cloneBytes(t.Calldata)
	return out
}

func (t Trigger) Call() TriggerCall {
	return TriggerCall{
		Trigger:  t.Clone(),
		GasLimit: t.GasLimit,
		Calldata: cloneBytes(t.Calldata),
	}
}

func (s *MemoryStateDB) RegisterTrigger(trigger Trigger) error {
	if err := trigger.Validate(); err != nil {
		return err
	}
	if trigger.GasLimit == 0 {
		return fmt.Errorf("missing trigger gas limit")
	}
	if s.triggers == nil {
		s.triggers = make(map[triggerKey]Trigger)
	}
	s.triggers[newTriggerKey(trigger.Contract, trigger.ID)] = trigger.Clone()
	return nil
}

func (s *MemoryStateDB) RemoveTrigger(contract ContractAddress, id string) {
	if s == nil || s.triggers == nil {
		return
	}
	delete(s.triggers, newTriggerKey(contract, id))
}

func (s *MemoryStateDB) Triggers() []Trigger {
	if s == nil {
		return nil
	}
	keys := sortedTriggerKeys(s.triggers)
	out := make([]Trigger, 0, len(keys))
	for _, key := range keys {
		out = append(out, s.triggers[key].Clone())
	}
	return out
}

func (s *MemoryStateDB) DueTriggerCalls(env BlockEnvironment) []TriggerCall {
	if s == nil {
		return nil
	}
	keys := sortedTriggerKeys(s.triggers)
	out := make([]TriggerCall, 0, len(keys))
	for _, key := range keys {
		trigger := s.triggers[key]
		if trigger.Due(env) {
			out = append(out, trigger.Call())
		}
	}
	return out
}

type triggerKey struct {
	contract EVMAddress
	id       string
}

func newTriggerKey(contract ContractAddress, id string) triggerKey {
	return triggerKey{contract: ContractAddressHash(contract), id: id}
}

func sortedTriggerKeys(triggers map[triggerKey]Trigger) []triggerKey {
	keys := make([]triggerKey, 0, len(triggers))
	for key := range triggers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if cmp := bytes.Compare(keys[i].contract[:], keys[j].contract[:]); cmp != 0 {
			return cmp < 0
		}
		return keys[i].id < keys[j].id
	})
	return keys
}

func cloneTriggers(src map[triggerKey]Trigger) map[triggerKey]Trigger {
	if src == nil {
		return nil
	}
	dst := make(map[triggerKey]Trigger, len(src))
	for key, trigger := range src {
		dst[key] = trigger.Clone()
	}
	return dst
}
