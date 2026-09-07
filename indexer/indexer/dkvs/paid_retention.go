package dkvs

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type PaidRecordRetention struct {
	CurrentBlock  uint64
	LastPayHeight uint64
}

type PaidRecordRetentionVerifier interface {
	PaidRecordRetention(record *wire.DKVSRecord, parsed ParsedKey) (PaidRecordRetention, error)
}

type paidRetentionCache struct {
	mutex   sync.RWMutex
	entries map[string]PaidRecordRetention
}

var paidRetentionCaches sync.Map

func paidRetentionCacheFor(indexer *Indexer) *paidRetentionCache {
	if indexer == nil {
		return nil
	}
	value, _ := paidRetentionCaches.LoadOrStore(indexer, &paidRetentionCache{entries: make(map[string]PaidRecordRetention)})
	return value.(*paidRetentionCache)
}

func (c *paidRetentionCache) replace(entries map[string]PaidRecordRetention) {
	if c == nil {
		return
	}
	c.mutex.Lock()
	c.entries = entries
	c.mutex.Unlock()
}

func (c *paidRetentionCache) get(key string) (PaidRecordRetention, bool) {
	if c == nil {
		return PaidRecordRetention{}, false
	}
	c.mutex.RLock()
	entry, ok := c.entries[key]
	c.mutex.RUnlock()
	return entry, ok
}

func (c *paidRetentionCache) set(key string, retention PaidRecordRetention) {
	if c == nil || key == "" {
		return
	}
	c.mutex.Lock()
	c.entries[key] = retention
	c.mutex.Unlock()
}

func (c *paidRetentionCache) remove(keys []string) {
	if c == nil || len(keys) == 0 {
		return
	}
	c.mutex.Lock()
	for _, key := range keys {
		delete(c.entries, key)
	}
	c.mutex.Unlock()
}

func (v LocalCacheAutopayFeeVerifier) PaidRecordRetention(record *wire.DKVSRecord, parsed ParsedKey) (PaidRecordRetention, error) {
	return v.AutopayFeeVerifier.PaidRecordRetention(record, parsed)
}

func (v AutopayFeeVerifier) PaidRecordRetention(record *wire.DKVSRecord, parsed ParsedKey) (PaidRecordRetention, error) {
	var retention PaidRecordRetention
	_ = parsed
	if record == nil || v.StateProvider == nil {
		return retention, ErrInvalidRecord
	}
	proof, err := ParseFeeProof(record.FeeProof)
	if err != nil || proof.Mode != FeeModeAutopay || strings.TrimSpace(proof.PoolContract) == "" {
		return retention, ErrInvalidFeeProof
	}
	if expected := strings.TrimSpace(v.Contract); expected != "" &&
		!strings.EqualFold(strings.TrimSpace(proof.PoolContract), expected) {
		return retention, ErrInvalidFeeProof
	}
	pubKey, err := RecordSignerPubKey(record)
	if err != nil {
		return retention, ErrInvalidFeeProof
	}
	payer, err := P2TRAddressFromPubKeyBytes(pubKey, v.AddressParams)
	if err != nil {
		return retention, ErrInvalidFeeProof
	}
	state, err := v.StateProvider.GetAutopayState(proof.PoolContract)
	if err != nil {
		return retention, err
	}
	if state == nil || strings.TrimSpace(state.TemplateName) != autopayTemplateName {
		return retention, ErrInvalidFeeProof
	}
	if expected := strings.TrimSpace(v.ServiceName); expected != "" &&
		!strings.EqualFold(strings.TrimSpace(state.ServiceName), expected) {
		return retention, ErrInvalidFeeProof
	}
	if expected := strings.TrimSpace(v.Recipient); expected != "" &&
		!strings.EqualFold(strings.TrimSpace(state.Recipient), expected) {
		return retention, ErrInvalidFeeProof
	}
	if expected := strings.TrimSpace(v.FeeAssetName); expected != "" &&
		strings.TrimSpace(state.FeeAssetName) != expected {
		return retention, ErrInvalidFeeProof
	}
	delegate, ok := state.Delegates[strings.TrimSpace(payer)]
	if !ok {
		return retention, ErrInvalidFeeProof
	}
	if _, err := positiveRat(delegate.AmountPerBlock); err != nil {
		return retention, err
	}
	if state.CurrentBlock <= 0 || delegate.LastPayHeight < 0 {
		return retention, ErrInvalidFeeProof
	}
	retention.CurrentBlock = uint64(state.CurrentBlock)
	retention.LastPayHeight = uint64(delegate.LastPayHeight)
	return retention, nil
}

func verifiedPaidRetentionAfterFeeVerification(record *wire.DKVSRecord, parsed ParsedKey,
	verifier FeeVerifier, height uint64) (*PaidRecordRetention, error) {
	if !isAutopayRecord(record) {
		return nil, nil
	}
	retentionVerifier, ok := verifier.(PaidRecordRetentionVerifier)
	if !ok {
		return nil, ErrInvalidFeeProof
	}
	retention, err := retentionVerifier.PaidRecordRetention(record, parsed)
	if err != nil {
		return nil, err
	}
	if !paidRetentionCurrent(retention, height) {
		return nil, ErrInvalidFeeProof
	}
	return &retention, nil
}

// primePaidRetentionAfterFeeVerification is retained for callers that commit
// immediately. Transactional paths should verify first and update this cache
// only after the database batch commits.
func (i *Indexer) primePaidRetentionAfterFeeVerification(record *wire.DKVSRecord, parsed ParsedKey,
	verifier FeeVerifier) error {
	retention, err := verifiedPaidRetentionAfterFeeVerification(record, parsed, verifier, i.currentHeight())
	if err != nil || retention == nil {
		return err
	}
	paidRetentionCacheFor(i).set(record.Key, *retention)
	return nil
}

func isAutopayRecord(record *wire.DKVSRecord) bool {
	if record == nil || len(record.FeeProof) == 0 {
		return false
	}
	proof, err := ParseFeeProof(record.FeeProof)
	return err == nil && proof.Mode == FeeModeAutopay
}

func paidRetentionGraceBlocks(policy FreeLocalCachePolicy) uint64 {
	return policy.MaxTTL
}

func paidRetentionExpired(retention PaidRecordRetention, height, graceBlocks uint64) bool {
	current := height
	if retention.CurrentBlock > current {
		current = retention.CurrentBlock
	}
	if current == 0 {
		return false
	}
	if retention.LastPayHeight >= current {
		return false
	}
	if retention.LastPayHeight == 0 {
		return true
	}
	if retention.LastPayHeight > ^uint64(0)-graceBlocks {
		return false
	}
	return current > retention.LastPayHeight+graceBlocks
}

func paidRetentionCurrent(retention PaidRecordRetention, height uint64) bool {
	current := height
	if retention.CurrentBlock > current {
		current = retention.CurrentBlock
	}
	return current != 0 && retention.LastPayHeight >= current
}

// paidRecordRelayable is intentionally cache-only. Contract state is refreshed
// outside the indexer lock once per block and by the periodic maintenance loop.
// A missing entry fails closed so records loaded after restart cannot relay until
// the current-block payment has been verified. Newly accepted records become
// relayable after the same refresh; local readability is unaffected.
func (i *Indexer) paidRecordRelayable(record *wire.DKVSRecord) bool {
	if !isAutopayRecord(record) {
		return true
	}
	retention, ok := paidRetentionCacheFor(i).get(record.Key)
	if !ok {
		return false
	}
	return paidRetentionCurrent(retention, i.currentHeight())
}

// RefreshPaidRetentionAt reads contract state without holding the indexer lock
// and atomically refreshes the local relay view for all AUTOPAY records.
func (i *Indexer) RefreshPaidRetentionAt(height uint64) error {
	if i == nil {
		return nil
	}
	validators := i.snapshotValidators()
	verifier, ok := validators.feeVerifier.(PaidRecordRetentionVerifier)
	if !ok {
		paidRetentionCacheFor(i).replace(make(map[string]PaidRecordRetention))
		return nil
	}
	records, _, _, err := i.scan("", nil, 0, false)
	if err != nil {
		return err
	}
	entries := make(map[string]PaidRecordRetention)
	var firstErr error
	for _, record := range records {
		if !isAutopayRecord(record) || IsTombstone(record.Flags) {
			continue
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			entries[record.Key] = PaidRecordRetention{CurrentBlock: height}
			continue
		}
		retention, err := verifier.PaidRecordRetention(record, parsed)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			entries[record.Key] = PaidRecordRetention{CurrentBlock: height}
			continue
		}
		entries[record.Key] = retention
	}
	paidRetentionCacheFor(i).replace(entries)
	// AUTOPAY records that stop paying become local cache entries. Rebuild free
	// cache accounting lazily on the next FREE_LOCAL capacity check.
	i.mutex.Lock()
	i.resetFreeLocalUsageLocked()
	i.mutex.Unlock()
	return firstErr
}

func (i *Indexer) RefreshPaidRetention() error {
	return i.RefreshPaidRetentionAt(i.currentHeight())
}

func (i *Indexer) PruneExpiredAutopay() (int, error) {
	return i.PruneExpiredAutopayAt(i.currentHeight())
}

// PruneExpiredAutopayAt physically removes AUTOPAY records after the payer has
// stopped paying and the node-local grace period has elapsed. The physical
// removal, sequence floor, canonical PathMeta update and EXPIRE cursor commit
// are persisted in one database batch.
func (i *Indexer) PruneExpiredAutopayAt(height uint64) (int, error) {
	if i == nil {
		return 0, nil
	}
	validators := i.snapshotValidators()
	verifier, ok := validators.feeVerifier.(PaidRecordRetentionVerifier)
	if !ok {
		return 0, nil
	}
	i.mutex.RLock()
	policy := i.freeLocal
	i.mutex.RUnlock()
	graceBlocks := paidRetentionGraceBlocks(policy)
	records, _, _, err := i.scan("", nil, 0, false)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		record *wire.DKVSRecord
		hash   chainhash.Hash
	}
	candidates := make([]candidate, 0)
	for _, record := range records {
		if !isAutopayRecord(record) || IsTombstone(record.Flags) {
			continue
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			continue
		}
		retention, err := verifier.PaidRecordRetention(record, parsed)
		if err != nil || !paidRetentionExpired(retention, height, graceBlocks) {
			continue
		}
		candidates = append(candidates, candidate{record: record, hash: RecordHash(record)})
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	i.mutex.Lock()
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	removed := make([]*wire.DKVSRecord, 0, len(candidates))
	for _, item := range candidates {
		current, readErr := i.getRaw(item.record.Key)
		if errors.Is(readErr, ErrRecordNotFound) {
			continue
		}
		if readErr != nil {
			i.mutex.Unlock()
			return 0, readErr
		}
		if RecordHash(current) != item.hash {
			continue
		}
		removed = append(removed, current)
	}
	if len(removed) == 0 {
		i.mutex.Unlock()
		return 0, nil
	}
	now := currentUnixMilli()
	expiryResult, err := i.stageExpiredRecordsLocked(batch, removed, height, now)
	if err != nil {
		i.mutex.Unlock()
		return 0, err
	}
	if err := batch.Flush(); err != nil {
		i.mutex.Unlock()
		return 0, err
	}
	removedKeys := make([]string, 0, len(removed))
	for _, record := range removed {
		removedKeys = append(removedKeys, record.Key)
		if i.feeUsageInitialized {
			i.removeFeeUsageLocked(record.Key)
		}
		if i.freeLocalUsageInitialized {
			i.removeFreeLocalUsageLocked(record.Key)
		}
		if i.recordExpiryInitialized {
			delete(i.recordExpiryEntries, record.Key)
		}
	}
	atomic.AddUint64(&i.generation, 1)
	i.mutex.Unlock()
	i.notifyExpiryCommit(expiryResult)
	paidRetentionCacheFor(i).remove(removedKeys)
	return len(removed), nil
}
