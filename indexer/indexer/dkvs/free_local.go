package dkvs

import "github.com/sat20-labs/satoshinet/wire"

type freeLocalUsage struct {
	records uint64
	bytes   uint64
}

type freeLocalUsageEntry struct {
	signer string
	bytes  uint64
}

func normalizeFreeLocalCachePolicy(policy FreeLocalCachePolicy, allow bool) FreeLocalCachePolicy {
	policy.Enabled = policy.Enabled && allow
	return policy
}

func isFreeLocalRecord(record *wire.DKVSRecord) bool {
	if record == nil || len(record.FeeProof) == 0 {
		return false
	}
	proof, err := ParseFeeProof(record.FeeProof)
	return err == nil && proof.Mode == FeeModeFreeLocal
}

// isLocalOnlyRecord only accepts the explicit FREE_LOCAL proof. Legacy empty
// proofs are intentionally left to the configured fee verifier so enabling a
// cache policy cannot silently change their historical relay behavior.
func (i *Indexer) isLocalOnlyRecord(record *wire.DKVSRecord) bool {
	return isFreeLocalRecord(record)
}

func (i *Indexer) relayableRecords(records []*wire.DKVSRecord) []*wire.DKVSRecord {
	filtered := make([]*wire.DKVSRecord, 0, len(records))
	for _, record := range records {
		if record != nil && !i.isLocalOnlyRecord(record) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func freeLocalSigner(record *wire.DKVSRecord, parsed ParsedKey) (string, error) {
	if record == nil {
		return "", ErrInvalidRecord
	}
	if len(record.PubKey) != 0 {
		return AccountID(record.PubKey), nil
	}
	return RecordSignerAccountID(record, parsed)
}

func (i *Indexer) resetFreeLocalUsageLocked() {
	i.freeLocalUsageInitialized = false
	i.freeLocalUsageBySigner = make(map[string]freeLocalUsage)
	i.freeLocalUsageEntries = make(map[string]freeLocalUsageEntry)
	i.freeLocalTotal = freeLocalUsage{}
}

func (i *Indexer) ensureFreeLocalUsageLocked(height, now uint64) error {
	if i.freeLocalUsageInitialized {
		return nil
	}
	i.resetFreeLocalUsageLocked()
	records, _, _, err := i.scanLocked("", nil, 0, false, height, now)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record == nil || IsExpired(record, height, now) || !i.isLocalOnlyRecord(record) {
			continue
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return err
		}
		signer, err := freeLocalSigner(record, parsed)
		if err != nil {
			return err
		}
		i.addFreeLocalUsageLocked(record, signer)
	}
	i.freeLocalUsageInitialized = true
	return nil
}

func (i *Indexer) addFreeLocalUsageLocked(record *wire.DKVSRecord, signer string) {
	if record == nil || signer == "" {
		return
	}
	size := uint64(RecordSize(record))
	usage := i.freeLocalUsageBySigner[signer]
	usage.records++
	usage.bytes += size
	i.freeLocalUsageBySigner[signer] = usage
	i.freeLocalUsageEntries[record.Key] = freeLocalUsageEntry{signer: signer, bytes: size}
	i.freeLocalTotal.records++
	i.freeLocalTotal.bytes += size
}

func (i *Indexer) removeFreeLocalUsageLocked(key string) {
	entry, ok := i.freeLocalUsageEntries[key]
	if !ok {
		return
	}
	delete(i.freeLocalUsageEntries, key)
	usage := i.freeLocalUsageBySigner[entry.signer]
	if usage.records <= 1 {
		usage.records = 0
	} else {
		usage.records--
	}
	if usage.bytes <= entry.bytes {
		usage.bytes = 0
	} else {
		usage.bytes -= entry.bytes
	}
	if usage.records == 0 {
		delete(i.freeLocalUsageBySigner, entry.signer)
	} else {
		i.freeLocalUsageBySigner[entry.signer] = usage
	}
	if i.freeLocalTotal.records > 0 {
		i.freeLocalTotal.records--
	}
	if i.freeLocalTotal.bytes <= entry.bytes {
		i.freeLocalTotal.bytes = 0
	} else {
		i.freeLocalTotal.bytes -= entry.bytes
	}
}

func (i *Indexer) replaceFreeLocalUsageLocked(record *wire.DKVSRecord, parsed ParsedKey) error {
	if !i.freeLocalUsageInitialized {
		return nil
	}
	i.removeFreeLocalUsageLocked(record.Key)
	if !i.isLocalOnlyRecord(record) {
		return nil
	}
	signer, err := freeLocalSigner(record, parsed)
	if err != nil {
		return err
	}
	i.addFreeLocalUsageLocked(record, signer)
	return nil
}

func (i *Indexer) validateFreeLocalCapacityLocked(record *wire.DKVSRecord, parsed ParsedKey, height, now uint64) error {
	if !i.isLocalOnlyRecord(record) {
		return nil
	}
	policy := i.freeLocal
	if !policy.Enabled {
		return ErrFreeLocalDisabled
	}
	if record.TTL == 0 || record.TTL > policy.MaxTTL {
		return ErrInvalidRecord
	}
	if err := i.ensureFreeLocalUsageLocked(height, now); err != nil {
		return err
	}
	signer, err := freeLocalSigner(record, parsed)
	if err != nil {
		return err
	}
	entry, replacing := i.freeLocalUsageEntries[record.Key]
	currentSigner := i.freeLocalUsageBySigner[signer]
	projectedSigner := currentSigner
	projectedTotal := i.freeLocalTotal
	if replacing {
		if entry.signer == signer {
			projectedSigner.bytes -= entry.bytes
		} else {
			projectedSigner.records++
		}
		if projectedTotal.bytes <= entry.bytes {
			projectedTotal.bytes = 0
		} else {
			projectedTotal.bytes -= entry.bytes
		}
	} else {
		projectedSigner.records++
		projectedTotal.records++
	}
	size := uint64(RecordSize(record))
	projectedSigner.bytes += size
	projectedTotal.bytes += size
	if policy.MaxRecordsPerSigner == 0 || projectedSigner.records > policy.MaxRecordsPerSigner ||
		policy.MaxBytesPerSigner == 0 || projectedSigner.bytes > policy.MaxBytesPerSigner ||
		policy.MaxTotalRecords == 0 || projectedTotal.records > policy.MaxTotalRecords ||
		policy.MaxTotalBytes == 0 || projectedTotal.bytes > policy.MaxTotalBytes {
		return ErrFreeLocalQuotaExceeded
	}
	return nil
}

func (i *Indexer) FreeLocalCachePolicy() FreeLocalCachePolicy {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.freeLocal
}
