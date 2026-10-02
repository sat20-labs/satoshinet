package rgb11names

import (
	"fmt"
	"strconv"
	"strings"
)

func (s *Index) Lookup(query Query) (*Result, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	txn := &transaction{state: s.state}
	result := &Result{Cursor: s.tip.Cursor}
	switch query.Kind {
	case "status":
		return result, nil
	case "contract":
		if !validContractID(query.Value) {
			return nil, ErrInvalid
		}
		var registration Registration
		if err := txn.get("contract/"+query.Value, &registration); err != nil {
			return nil, err
		}
		result.Registration = &registration
	case "name":
		if !strings.HasPrefix(query.Value, "rgb11:") || len(query.Value) > MaxTickerBytes+128 {
			return nil, ErrInvalid
		}
		var id string
		if err := txn.get("name/"+query.Value, &id); err != nil {
			return nil, err
		}
		var registration Registration
		if err := txn.get("contract/"+id, &registration); err != nil {
			return nil, ErrCorrupt
		}
		if registration.AssetName != query.Value {
			return nil, ErrCorrupt
		}
		result.Registration = &registration
	case "counter":
		if ValidateDID(query.Provider) != nil {
			return nil, ErrInvalid
		}
		base, err := NormalizeTicker(query.Ticker)
		if err != nil {
			return nil, err
		}
		var counter uint64
		if err := txn.get("counter/"+query.Provider+"/"+base, &counter); err != nil && err != ErrNotFound {
			return nil, err
		}
		result.Counter = &Counter{ProviderDID: query.Provider, BaseTicker: base, MaxOrdinal: counter}
	default:
		return nil, ErrInvalid
	}
	return result, nil
}

func validPosition(position Position, cursor Cursor) bool {
	return position.Height >= 0 && position.Height <= cursor.Height && position.TxIndex > 0 && validHash(position.TxID)
}

// CheckSelf verifies both mapping directions and exact ordinal continuity.
func (s *Index) CheckSelf() error {
	if s == nil {
		return ErrUnavailable
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := &transaction{state: s.state}
	maximum := make(map[string]uint64)
	counts := make(map[string]uint64)
	ordinals := make(map[string]bool)
	for key := range s.state {
		switch {
		case key == cursorKey:
			var persisted checkpoint
			if t.get(key, &persisted) != nil || persisted != s.tip || !validHash(persisted.EventsHash) {
				return ErrCorrupt
			}
		case strings.HasPrefix(key, "contract/"):
			var record Registration
			if t.get(key, &record) != nil || key != "contract/"+record.ContractID ||
				!validContractID(record.ContractID) || !validOutpoint(record.GenesisOutpoint) ||
				!validPosition(record.RegisteredAt, s.tip.Cursor) || ValidateDID(record.ProviderDID) != nil {
				return ErrCorrupt
			}
			base, err := NormalizeTicker(record.BaseTicker)
			if err != nil || base != record.BaseTicker {
				return ErrCorrupt
			}
			name, err := BuildAssetName(record.BaseTicker, record.AssetType, record.ProviderDID, record.Ordinal)
			if err != nil || name != record.AssetName {
				return ErrCorrupt
			}
			var id string
			if t.get("name/"+name, &id) != nil || id != record.ContractID {
				return ErrCorrupt
			}
			ns := "counter/" + record.ProviderDID + "/" + base
			ordinalKey := ns + "/" + strconv.FormatUint(record.Ordinal, 10)
			if ordinals[ordinalKey] {
				return ErrCorrupt
			}
			ordinals[ordinalKey] = true
			counts[ns]++
			if record.Ordinal > maximum[ns] {
				maximum[ns] = record.Ordinal
			}
		case strings.HasPrefix(key, "name/"):
			var id string
			var record Registration
			if t.get(key, &id) != nil || t.get("contract/"+id, &record) != nil || key != "name/"+record.AssetName {
				return ErrCorrupt
			}
		case strings.HasPrefix(key, "counter/"):
			// Checked against registrations below.
		default:
			return fmt.Errorf("%w: unknown key %q", ErrCorrupt, key)
		}
	}
	for ns, max := range maximum {
		var counter uint64
		if t.get(ns, &counter) != nil || counter != max || counts[ns] != max {
			return ErrCorrupt
		}
	}
	for key := range s.state {
		if strings.HasPrefix(key, "counter/") {
			if _, ok := maximum[key]; !ok {
				return ErrCorrupt
			}
		}
	}
	return nil
}

func (s *Index) HasEffects() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for key := range s.state {
		if key != cursorKey {
			return true
		}
	}
	return false
}
