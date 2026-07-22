package dkvs

import (
	"errors"
	"strings"
	"sync/atomic"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const satsNetTargetBlockMillis = uint64(12_000)

type PaidRecordRetention struct {
	CurrentBlock  uint64
	LastPayHeight uint64
}

type PaidRecordRetentionVerifier interface {
	PaidRecordRetention(record *wire.DKVSRecord, parsed ParsedKey) (PaidRecordRetention, error)
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
	if state == nil || strings.TrimSpace(state.TemplateName) != autopayTemplateName || state.Closed ||
		strings.EqualFold(strings.TrimSpace(state.Status), "closed") ||
		strings.EqualFold(strings.TrimSpace(state.Status), "expired") {
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

func isAutopayRecord(record *wire.DKVSRecord) bool {
	if record == nil || len(record.FeeProof) == 0 {
		return false
	}
	proof, err := ParseFeeProof(record.FeeProof)
	return err == nil && proof.Mode == FeeModeAutopay
}

func paidRetentionGraceBlocks(policy FreeLocalCachePolicy) uint64 {
	if policy.MaxTTL == 0 {
		return 0
	}
	return (policy.MaxTTL + satsNetTargetBlockMillis - 1) / satsNetTargetBlockMillis
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

func (i *Indexer) PruneExpiredAutopay() (int, error) {
	return i.PruneExpiredAutopayAt(i.currentHeight())
}

// PruneExpiredAutopayAt physically removes AUTOPAY records after their payer
// stopped paying and the node-local cache grace period elapsed. The grace
// period is derived from the same policy exposed by GET /v3/dkvs/config.
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
		current, err := i.getRaw(item.record.Key)
		if errors.Is(err, ErrRecordNotFound) || errors.Is(err, indexercommon.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			i.mutex.Unlock()
			return 0, err
		}
		if RecordHash(current) != item.hash {
			continue
		}
		if err := batch.Delete(recordDBKey(current.Key)); err != nil {
			i.mutex.Unlock()
			return 0, err
		}
		if err := batch.Delete(hashDBKey(item.hash)); err != nil {
			i.mutex.Unlock()
			return 0, err
		}
		removed = append(removed, current)
	}
	if len(removed) == 0 {
		i.mutex.Unlock()
		return 0, nil
	}
	if err := i.markPathMetaDirtyLocked(batch, removed, height, currentUnixMilli()); err != nil {
		i.mutex.Unlock()
		return 0, err
	}
	if err := batch.Flush(); err != nil {
		i.mutex.Unlock()
		return 0, err
	}
	for _, record := range removed {
		if i.feeUsageInitialized {
			i.removeFeeUsageLocked(record.Key)
		}
		if i.recordExpiryInitialized {
			delete(i.recordExpiryEntries, record.Key)
		}
	}
	atomic.AddUint64(&i.generation, 1)
	i.mutex.Unlock()
	return len(removed), nil
}
