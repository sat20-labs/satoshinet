package rgb11names

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	idx "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

const dbPrefix = "rgb11names:"
const cursorKey = "cursor"

type checkpoint struct {
	Cursor
	EventsHash string `json:"events_hash"`
}

// Index keeps compact naming metadata, not UTXOs, balances or RGB consignments.
// Stored byte slices are immutable. Clones copy map membership and therefore
// remain genuine read snapshots even after a newer live view is flushed.
type Index struct {
	mu     sync.RWMutex
	params *chaincfg.Params
	state  map[string][]byte
	dirty  map[string][]byte
	tip    checkpoint
	parent *Index
}

func Open(database idx.KVDB, params *chaincfg.Params, cursor Cursor) (*Index, error) {
	if database == nil || params == nil || cursor.Height < -1 || (cursor.Height >= 0 && !validHash(cursor.Hash)) || (cursor.Height == -1 && cursor.Hash != "") {
		return nil, ErrInvalid
	}
	s := &Index{params: params, state: make(map[string][]byte), dirty: make(map[string][]byte), tip: checkpoint{Cursor: cursor}}
	if err := database.BatchRead([]byte(dbPrefix), false, func(k, v []byte) error {
		key := strings.TrimPrefix(string(k), dbPrefix)
		if key == string(k) || key == "" || len(v) == 0 {
			return ErrCorrupt
		}
		s.state[key] = append([]byte(nil), v...)
		return nil
	}); err != nil {
		return nil, err
	}
	if raw, ok := s.state[cursorKey]; ok {
		if json.Unmarshal(raw, &s.tip) != nil || s.tip.Cursor != cursor {
			return nil, fmt.Errorf("%w: naming/base checkpoints differ", ErrCorrupt)
		}
	} else if len(s.state) != 0 {
		return nil, ErrCorrupt
	}
	if err := s.CheckSelf(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Index) Clone() *Index {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	clone := &Index{params: s.params, state: make(map[string][]byte, len(s.state)), dirty: make(map[string][]byte, len(s.dirty)), tip: s.tip, parent: s}
	for k, v := range s.state {
		clone.state[k] = v
	}
	for k, v := range s.dirty {
		clone.dirty[k] = v
	}
	return clone
}

type transaction struct {
	state  map[string][]byte
	writes map[string][]byte
}

func (t *transaction) get(key string, out any) error {
	raw, ok := t.writes[key]
	if !ok {
		raw, ok = t.state[key]
	}
	if !ok || raw == nil {
		return ErrNotFound
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: %s", ErrCorrupt, key)
	}
	return nil
}

func (t *transaction) put(key string, value any) error {
	raw, err := json.Marshal(value)
	if err == nil {
		t.writes[key] = raw
	}
	return err
}

// ApplyBlock is all-or-nothing. A failed event consumes neither an ordinal nor
// the block cursor. Same-block replay is accepted only for identical effects.
// It does NOT authenticate EventSource's data or create any asset balance.
func (s *Index) ApplyBlock(block *common.Block, events []Event) error {
	if s == nil || block == nil || block.Height < 0 || !validHash(block.Hash) || len(events) > MaxBlockEvents {
		return ErrInvalid
	}
	if len(events) == 0 {
		events = []Event{}
	}
	raw, err := json.Marshal(events)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	eventsHash := hex.EncodeToString(digest[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if block.Height == s.tip.Height {
		if block.Hash == s.tip.Hash && eventsHash == s.tip.EventsHash {
			return nil
		}
		return ErrOrder
	}
	if block.Height != s.tip.Height+1 || (s.tip.Height >= 0 && block.PrevBlockHash != s.tip.Hash) {
		return ErrOrder
	}
	txn := &transaction{state: s.state, writes: make(map[string][]byte)}
	for n, event := range events {
		if int64(event.TxIndex) >= int64(len(block.Transactions)) || block.Transactions[event.TxIndex] == nil ||
			event.TxID != block.Transactions[event.TxIndex].Txid || !validHash(event.TxID) {
			return ErrOrder
		}
		if n > 0 {
			prev := events[n-1]
			if event.TxIndex < prev.TxIndex || event.TxIndex == prev.TxIndex && event.EventIndex <= prev.EventIndex {
				return ErrOrder
			}
		}
		count := 0
		if event.Ownership != nil {
			count++
		}
		if event.Bind != nil {
			count++
		}
		if event.Register != nil {
			count++
		}
		if count != 1 || event.TxIndex == 0 && event.Ownership == nil {
			return ErrInvalid
		}
		position := Position{Height: block.Height, TxID: event.TxID, TxIndex: event.TxIndex, EventIndex: event.EventIndex}
		switch {
		case event.Ownership != nil:
			err = s.applyOwnership(txn, *event.Ownership)
		case event.Bind != nil:
			err = s.applyBind(txn, *event.Bind, position)
		case event.Register != nil:
			err = s.applyRegistration(txn, *event.Register, position)
		}
		if err != nil {
			if event.Optional && event.Register != nil &&
				(errors.Is(err, ErrNotFound) || errors.Is(err, ErrOwner) ||
					errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid)) {
				// Contract deployment remains valid even when it cannot acquire a
				// canonical RGB11 name (for example, no valid Primary DID bind).
				continue
			}
			return fmt.Errorf("RGB11 naming event %d/%d: %w", event.TxIndex, event.EventIndex, err)
		}
	}
	next := checkpoint{Cursor: Cursor{Height: block.Height, Hash: block.Hash}, EventsHash: eventsHash}
	if err := txn.put(cursorKey, next); err != nil {
		return err
	}
	for k, v := range txn.writes {
		s.state[k] = v
		s.dirty[k] = v
	}
	s.tip = next
	return nil
}

func (s *Index) applyOwnership(t *transaction, fact Ownership) error {
	if ValidateDID(fact.DID) != nil || fact.OwnerUtxo == "" || !validOutpoint(fact.OwnerUtxo) || fact.OwnerSat < 0 {
		return ErrInvalid
	}
	if fact.Address != "" {
		address, err := canonicalAddress(fact.Address, s.params)
		if err != nil {
			return err
		}
		fact.Address = address
	}
	var old Ownership
	if err := t.get("owner/"+fact.DID, &old); err == nil {
		// The DID identity itself is immutable. A transfer changes OwnerUtxo,
		// while inscription ID and sat (when supplied) remain stable.
		if old.InscriptionID != "" && fact.InscriptionID != "" && old.InscriptionID != fact.InscriptionID {
			return ErrOwner
		}
		if old.OwnerSat != 0 && fact.OwnerSat != 0 && old.OwnerSat != fact.OwnerSat {
			return ErrOwner
		}
		if old.OwnerUtxo == fact.OwnerUtxo && old.Address != fact.Address {
			return ErrOwner
		}
	} else if err != ErrNotFound {
		return err
	}
	return t.put("owner/"+fact.DID, fact)
}

func (s *Index) applyBind(t *transaction, request Bind, at Position) error {
	if ValidateDID(request.DID) != nil {
		return ErrInvalid
	}
	address, err := canonicalAddress(request.Address, s.params)
	if err != nil {
		return err
	}
	var owner Ownership
	if err := t.get("owner/"+request.DID, &owner); err != nil {
		return err
	}
	if owner.Address != address || owner.OwnerUtxo == "" {
		return ErrOwner
	}
	var old Binding
	if err := t.get("bind/"+address, &old); err == nil {
		if old.DID == owner.DID && old.OwnerUtxo == owner.OwnerUtxo {
			return nil
		}
	} else if err != ErrNotFound {
		return err
	}
	return t.put("bind/"+address, Binding{
		DID: owner.DID, Address: address, OwnerUtxo: owner.OwnerUtxo,
		OwnerSat: owner.OwnerSat, InscriptionID: owner.InscriptionID, BoundAt: at,
	})
}

func activeBinding(t *transaction, address string) (Binding, Ownership, error) {
	var binding Binding
	var owner Ownership
	if err := t.get("bind/"+address, &binding); err != nil {
		return binding, owner, err
	}
	if err := t.get("owner/"+binding.DID, &owner); err != nil {
		return binding, owner, err
	}
	if owner.Address != address || owner.OwnerUtxo == "" || owner.OwnerUtxo != binding.OwnerUtxo {
		return binding, owner, ErrNotFound
	}
	return binding, owner, nil
}

func (s *Index) applyRegistration(t *transaction, request Register, at Position) error {
	base, err := NormalizeTicker(request.BaseTicker)
	if err != nil || !validContractID(request.ContractID) || !validOutpoint(request.GenesisOutpoint) || (request.AssetType != "f" && request.AssetType != "n") {
		return ErrInvalid
	}
	address, err := canonicalAddress(request.GenesisAddress, s.params)
	if err != nil {
		return err
	}
	var old Registration
	if err := t.get("contract/"+request.ContractID, &old); err == nil {
		if old.BaseTicker != base || old.AssetType != request.AssetType || old.GenesisOutpoint != request.GenesisOutpoint || old.GenesisAddress != address {
			return ErrConflict
		}
		// A second transcend deployment/deposit reuses the immutable name.
		return nil
	} else if err != ErrNotFound {
		return err
	}
	binding, owner, err := activeBinding(t, address)
	if err != nil {
		return err
	}
	key := "counter/" + binding.DID + "/" + base
	var counter uint64
	if err := t.get(key, &counter); err != nil && err != ErrNotFound {
		return err
	}
	if counter == math.MaxUint64 {
		return ErrOrdinalLimit
	}
	ordinal := counter + 1
	name, err := BuildAssetName(base, request.AssetType, binding.DID, ordinal)
	if err != nil {
		return err
	}
	var collision string
	if err := t.get("name/"+name, &collision); err == nil {
		return ErrConflict
	} else if err != ErrNotFound {
		return err
	}
	providerSat := uint64(0)
	if owner.OwnerSat > 0 {
		providerSat = uint64(owner.OwnerSat)
	}
	registration := Registration{
		ContractID: request.ContractID, AssetName: name, BaseTicker: base,
		AssetType: request.AssetType, ProviderDID: binding.DID, ProviderSat: providerSat,
		Ordinal: ordinal, GenesisOutpoint: request.GenesisOutpoint,
		GenesisAddress: address, RegisteredAt: at,
	}
	if err := t.put("contract/"+request.ContractID, registration); err != nil {
		return err
	}
	if err := t.put("name/"+name, request.ContractID); err != nil {
		return err
	}
	return t.put(key, ordinal)
}

// Stage writes into the BASE INDEXER'S batch. Invoke the returned function only
// after that batch successfully Flushes. This commits names, reverse mappings,
// counters and the base sync-height together; failed IO retains pending changes.
// Do not Subtract naming deltas before flushing a backup snapshot.
func (s *Index) Stage(batch idx.WriteBatch) (func(), error) {
	if s == nil || batch == nil {
		return nil, ErrUnavailable
	}
	s.mu.RLock()
	staged := make(map[string][]byte, len(s.dirty))
	for k, v := range s.dirty {
		staged[k] = v
	}
	parent := s.parent
	s.mu.RUnlock()
	keys := make([]string, 0, len(staged))
	for k := range staged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := batch.Put([]byte(dbPrefix+k), append([]byte(nil), staged[k]...)); err != nil {
			return nil, err
		}
	}
	return func() {
		s.acknowledge(staged)
		if parent != nil {
			parent.acknowledge(staged)
		}
	}, nil
}

func (s *Index) acknowledge(staged map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range staged {
		if current, ok := s.dirty[k]; ok && bytes.Equal(current, v) {
			delete(s.dirty, k)
		}
	}
}
