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

// Index keeps compact naming metadata, not UTXOs, balances, RGB consignments
// or Primary DID state. Primary DID is owned by DKVS /personal metadata.
type Index struct {
	mu     sync.RWMutex
	params *chaincfg.Params
	state  map[string][]byte
	dirty  map[string][]byte
	tip    checkpoint
	parent *Index
}

func Open(database idx.KVDB, params *chaincfg.Params, cursor Cursor) (*Index, error) {
	if database == nil || params == nil || cursor.Height < -1 ||
		(cursor.Height >= 0 && !validHash(cursor.Hash)) || (cursor.Height == -1 && cursor.Hash != "") {
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
	clone := &Index{
		params: s.params, state: make(map[string][]byte, len(s.state)),
		dirty: make(map[string][]byte, len(s.dirty)), tip: s.tip, parent: s,
	}
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

// ApplyBlock is all-or-nothing for naming state. A failed storage/integrity
// event consumes neither an ordinal nor the block cursor. Naming-policy errors
// from auto-discovered transcend deployments are optional and never invalidate
// the underlying SatoshiNet transaction.
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
			event.TxID != block.Transactions[event.TxIndex].Txid || !validHash(event.TxID) || event.TxIndex == 0 ||
			event.Register == nil {
			return ErrOrder
		}
		if n > 0 {
			prev := events[n-1]
			if event.TxIndex < prev.TxIndex || (event.TxIndex == prev.TxIndex && event.EventIndex <= prev.EventIndex) {
				return ErrOrder
			}
		}
		position := Position{Height: block.Height, TxID: event.TxID, TxIndex: event.TxIndex, EventIndex: event.EventIndex}
		err = s.applyRegistration(txn, *event.Register, position)
		if err != nil {
			if event.Optional && (errors.Is(err, ErrInvalid) || errors.Is(err, ErrConflict) || errors.Is(err, ErrOrdinalLimit)) {
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

func (s *Index) applyRegistration(t *transaction, request Register, at Position) error {
	base, err := NormalizeTicker(request.BaseTicker)
	if err != nil || !validContractID(request.ContractID) || !validOutpoint(request.GenesisOutpoint) ||
		(request.AssetType != "f" && request.AssetType != "n") || ValidateDID(request.ProviderDID) != nil {
		return ErrInvalid
	}
	var old Registration
	if err := t.get("contract/"+request.ContractID, &old); err == nil {
		if old.BaseTicker != base || old.AssetType != request.AssetType ||
			old.GenesisOutpoint != request.GenesisOutpoint || old.ProviderDID != request.ProviderDID {
			return ErrConflict
		}
		return nil
	} else if err != ErrNotFound {
		return err
	}

	key := "counter/" + request.ProviderDID + "/" + base
	var counter uint64
	if err := t.get(key, &counter); err != nil && err != ErrNotFound {
		return err
	}
	if counter == math.MaxUint64 {
		return ErrOrdinalLimit
	}
	ordinal := counter + 1
	name, err := BuildAssetName(base, request.AssetType, request.ProviderDID, ordinal)
	if err != nil {
		return err
	}
	var collision string
	if err := t.get("name/"+name, &collision); err == nil {
		return ErrConflict
	} else if err != ErrNotFound {
		return err
	}
	registration := Registration{
		ContractID: request.ContractID, AssetName: name, BaseTicker: base,
		AssetType: request.AssetType, ProviderDID: request.ProviderDID,
		Ordinal: ordinal, GenesisOutpoint: request.GenesisOutpoint, RegisteredAt: at,
	}
	if err := t.put("contract/"+request.ContractID, registration); err != nil {
		return err
	}
	if err := t.put("name/"+name, request.ContractID); err != nil {
		return err
	}
	return t.put(key, ordinal)
}

// Stage writes into the BaseIndexer's batch. Naming mappings, counters and the
// base sync-height therefore commit atomically.
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
