package dkvs

import (
	"bytes"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

// DeleteCommand creates an UNSIGNED operation bound to exactly one current
// record. The caller must sign it with the presently authorized wallet. The
// command is never stored as data or replay history. Binding the target hash
// prevents a delayed delete from removing a same-key recreation in the same
// block (IssueHeight and Seq alone cannot distinguish that case).
func DeleteCommand(current *wire.DKVSRecord, issueHeight uint64) (*wire.DKVSRecord, error) {
	if current == nil || IsTombstone(current.Flags) || current.Seq == ^uint64(0) { return nil, ErrInvalidRecord }
	hash := RecordHash(current)
	return NewRecord(current.Key, hash[:], current.PubKey, RecordOptions{
		Seq: current.Seq+1, IssueHeight: issueHeight, Flags: FlagTombstone,
	})
}

func DeleteTargetHash(command *wire.DKVSRecord) (chainhash.Hash, error) {
	if command == nil || !IsTombstone(command.Flags) || len(command.Value) != chainhash.HashSize || command.TTL != 0 {
		return chainhash.Hash{}, ErrInvalidRecord
	}
	var hash chainhash.Hash
	copy(hash[:], command.Value)
	return hash, nil
}

func deleteTargetsRecord(command, current *wire.DKVSRecord) bool {
	if current == nil { return false }
	hash, err := DeleteTargetHash(command)
	return err == nil && hash == RecordHash(current) && current.Seq != ^uint64(0) && command.Seq == current.Seq+1
}

// CompareCurrentRecords is a freshness check, not a deletion history. For
// endpoint-local leases, a later signed IssueHeight can start a new lifetime.
// Live same-lifetime SDK writes still have to satisfy exact CAS and next Seq.
func CompareCurrentRecords(a, b *wire.DKVSRecord) int {
	if a == nil { if b == nil { return 0 }; return -1 }
	if b == nil { return 1 }
	if isFreeLocalRecord(a) && isFreeLocalRecord(b) && a.IssueHeight != b.IssueHeight {
		if a.IssueHeight < b.IssueHeight { return -1 }; return 1
	}
	if a.Seq != b.Seq { if a.Seq < b.Seq { return -1 }; return 1 }
	if a.IssueHeight != b.IssueHeight { if a.IssueHeight < b.IssueHeight { return -1 }; return 1 }
	ha, hb := RecordHash(a), RecordHash(b)
	return bytes.Compare(ha[:], hb[:])
}
