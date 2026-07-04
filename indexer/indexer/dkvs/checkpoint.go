package dkvs

import (
	"bytes"
	"encoding/hex"
	"sort"

	"github.com/sat20-labs/satoshinet/wire"
)

func SystemParamsKey() string {
	return "/sys/params"
}

func SystemMinerKey(minerID string) (string, error) {
	key := "/sys/miner/" + minerID
	_, err := ParseKey(key)
	return key, err
}

func SystemPoolKey(poolID string) (string, error) {
	key := "/sys/pool/" + poolID
	_, err := ParseKey(key)
	return key, err
}

func SnapshotHash(snapshot *Snapshot) string {
	if snapshot == nil || snapshot.Checkpoint == nil {
		return ""
	}
	hashes := make([][]byte, 0, len(snapshot.Records))
	for _, record := range snapshot.Records {
		hash := RecordHash(record)
		hashes = append(hashes, append([]byte{}, hash[:]...))
	}
	sort.Slice(hashes, func(i, j int) bool {
		return bytes.Compare(hashes[i], hashes[j]) < 0
	})
	var buf bytes.Buffer
	writeUint64(&buf, snapshot.Checkpoint.Height)
	writeUint64(&buf, snapshot.Checkpoint.ActiveRecordCount)
	writeUint64(&buf, snapshot.Checkpoint.ActiveRecordTotalSize)
	writeString(&buf, snapshot.Checkpoint.ActiveRecordRoot)
	writeUint64(&buf, snapshot.CreatedAt)
	namespaces := make([]string, 0, len(snapshot.Checkpoint.NamespaceRoots))
	for namespace := range snapshot.Checkpoint.NamespaceRoots {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	for _, namespace := range namespaces {
		writeString(&buf, namespace)
		writeString(&buf, snapshot.Checkpoint.NamespaceRoots[namespace])
	}
	for _, hash := range hashes {
		writeBytes(&buf, hash)
	}
	sum := RecordHash(&wire.DKVSRecord{
		Version: Version,
		Key:     "/sys/snapshot/hash",
		Value:   buf.Bytes(),
	})
	return hex.EncodeToString(sum[:])
}
