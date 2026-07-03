package dkvs

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/wire"
)

func CheckpointKey(epoch string) (string, error) {
	key := "/sys/checkpoint/" + epoch
	_, err := ParseKey(key)
	return key, err
}

func SnapshotKey(epoch string) (string, error) {
	key := "/sys/snapshot/" + epoch
	_, err := ParseKey(key)
	return key, err
}

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

func NewSignedCheckpoint(checkpoint *Checkpoint, epoch, createdBy string, priv *btcec.PrivateKey) (*SignedCheckpoint, error) {
	if checkpoint == nil || priv == nil || epoch == "" {
		return nil, ErrInvalidCheckpoint
	}
	if _, err := CheckpointKey(epoch); err != nil {
		return nil, err
	}
	if createdBy == "" {
		createdBy = hex.EncodeToString(priv.PubKey().SerializeCompressed())
	}
	signed := &SignedCheckpoint{
		Epoch:                 epoch,
		Height:                checkpoint.Height,
		ActiveRecordCount:     checkpoint.ActiveRecordCount,
		ActiveRecordTotalSize: checkpoint.ActiveRecordTotalSize,
		NamespaceRoots:        copyStringMap(checkpoint.NamespaceRoots),
		ActiveRecordRoot:      checkpoint.ActiveRecordRoot,
		CreatedBy:             createdBy,
	}
	hash := signedCheckpointHash(signed)
	signed.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
	return signed, nil
}

func BuildSignedCheckpointRecord(priv *btcec.PrivateKey, checkpoint *Checkpoint, epoch, createdBy string, opts RecordOptions) (*wire.DKVSRecord, *SignedCheckpoint, error) {
	signed, err := NewSignedCheckpoint(checkpoint, epoch, createdBy, priv)
	if err != nil {
		return nil, nil, err
	}
	value, err := json.Marshal(signed)
	if err != nil {
		return nil, nil, err
	}
	key, err := CheckpointKey(epoch)
	if err != nil {
		return nil, nil, err
	}
	record, err := NewSignedRecord(priv, key, value, opts)
	if err != nil {
		return nil, nil, err
	}
	return record, signed, nil
}

func ParseSignedCheckpointValue(value []byte) (*SignedCheckpoint, error) {
	var checkpoint SignedCheckpoint
	if len(value) == 0 || json.Unmarshal(value, &checkpoint) != nil {
		return nil, ErrInvalidCheckpoint
	}
	if checkpoint.Epoch == "" || checkpoint.ActiveRecordRoot == "" || len(checkpoint.Signature) == 0 {
		return nil, ErrInvalidCheckpoint
	}
	if _, err := CheckpointKey(checkpoint.Epoch); err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

func VerifySignedCheckpointValue(value []byte, pubKey []byte) (*SignedCheckpoint, error) {
	checkpoint, err := ParseSignedCheckpointValue(value)
	if err != nil {
		return nil, err
	}
	if len(pubKey) == 0 {
		pubKey, err = hex.DecodeString(checkpoint.CreatedBy)
		if err != nil {
			return nil, ErrInvalidCheckpoint
		}
	}
	if checkpoint.CreatedBy != "" {
		if createdBy, err := hex.DecodeString(checkpoint.CreatedBy); err == nil && !bytes.Equal(createdBy, pubKey) {
			return nil, ErrInvalidCheckpoint
		}
	}
	key, err := btcec.ParsePubKey(pubKey)
	if err != nil {
		return nil, err
	}
	sig, err := ecdsa.ParseSignature(checkpoint.Signature)
	if err != nil {
		return nil, err
	}
	hash := signedCheckpointHash(checkpoint)
	if !sig.Verify(hash[:], key) {
		return nil, ErrInvalidCheckpoint
	}
	return checkpoint, nil
}

func NewSignedSnapshot(snapshot *Snapshot, epoch, createdBy string, priv *btcec.PrivateKey) (*SignedSnapshot, error) {
	if snapshot == nil || snapshot.Checkpoint == nil || priv == nil || epoch == "" {
		return nil, ErrInvalidSnapshot
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		return nil, err
	}
	if _, err := SnapshotKey(epoch); err != nil {
		return nil, err
	}
	if createdBy == "" {
		createdBy = hex.EncodeToString(priv.PubKey().SerializeCompressed())
	}
	signed := &SignedSnapshot{
		Epoch:                 epoch,
		Height:                snapshot.Checkpoint.Height,
		ActiveRecordCount:     snapshot.Checkpoint.ActiveRecordCount,
		ActiveRecordTotalSize: snapshot.Checkpoint.ActiveRecordTotalSize,
		NamespaceRoots:        copyStringMap(snapshot.Checkpoint.NamespaceRoots),
		ActiveRecordRoot:      snapshot.Checkpoint.ActiveRecordRoot,
		SnapshotHash:          SnapshotHash(snapshot),
		CreatedAt:             snapshot.CreatedAt,
		CreatedBy:             createdBy,
	}
	hash := signedSnapshotHash(signed)
	signed.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
	return signed, nil
}

func BuildSignedSnapshotRecord(priv *btcec.PrivateKey, snapshot *Snapshot, epoch, createdBy string, opts RecordOptions) (*wire.DKVSRecord, *SignedSnapshot, error) {
	signed, err := NewSignedSnapshot(snapshot, epoch, createdBy, priv)
	if err != nil {
		return nil, nil, err
	}
	value, err := json.Marshal(signed)
	if err != nil {
		return nil, nil, err
	}
	key, err := SnapshotKey(epoch)
	if err != nil {
		return nil, nil, err
	}
	record, err := NewSignedRecord(priv, key, value, opts)
	if err != nil {
		return nil, nil, err
	}
	return record, signed, nil
}

func ParseSignedSnapshotValue(value []byte) (*SignedSnapshot, error) {
	var snapshot SignedSnapshot
	if len(value) == 0 || json.Unmarshal(value, &snapshot) != nil {
		return nil, ErrInvalidSnapshot
	}
	if snapshot.Epoch == "" || snapshot.ActiveRecordRoot == "" || snapshot.SnapshotHash == "" ||
		snapshot.CreatedBy == "" || len(snapshot.Signature) == 0 {
		return nil, ErrInvalidSnapshot
	}
	if _, err := SnapshotKey(snapshot.Epoch); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func VerifySignedSnapshotValue(value []byte, pubKey []byte) (*SignedSnapshot, error) {
	snapshot, err := ParseSignedSnapshotValue(value)
	if err != nil {
		return nil, err
	}
	if len(pubKey) == 0 {
		pubKey, err = hex.DecodeString(snapshot.CreatedBy)
		if err != nil {
			return nil, ErrInvalidSnapshot
		}
	}
	if createdBy, err := hex.DecodeString(snapshot.CreatedBy); err != nil || !bytes.Equal(createdBy, pubKey) {
		return nil, ErrInvalidSnapshot
	}
	key, err := btcec.ParsePubKey(pubKey)
	if err != nil {
		return nil, err
	}
	sig, err := ecdsa.ParseSignature(snapshot.Signature)
	if err != nil {
		return nil, err
	}
	hash := signedSnapshotHash(snapshot)
	if !sig.Verify(hash[:], key) {
		return nil, ErrInvalidSnapshot
	}
	return snapshot, nil
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

func signedCheckpointHash(checkpoint *SignedCheckpoint) [32]byte {
	return SigningHash(&wire.DKVSRecord{
		Version: Version,
		Key:     "/sys/checkpoint/" + checkpoint.Epoch,
		Value:   canonicalSignedCheckpointBytes(checkpoint),
		PubKey:  []byte(checkpoint.CreatedBy),
	})
}

func canonicalSignedCheckpointBytes(checkpoint *SignedCheckpoint) []byte {
	var buf bytes.Buffer
	writeString(&buf, checkpoint.Epoch)
	writeUint64(&buf, checkpoint.Height)
	writeUint64(&buf, checkpoint.ActiveRecordCount)
	writeUint64(&buf, checkpoint.ActiveRecordTotalSize)
	writeString(&buf, checkpoint.ActiveRecordRoot)
	writeString(&buf, checkpoint.CreatedBy)
	namespaces := make([]string, 0, len(checkpoint.NamespaceRoots))
	for namespace := range checkpoint.NamespaceRoots {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	for _, namespace := range namespaces {
		writeString(&buf, namespace)
		writeString(&buf, checkpoint.NamespaceRoots[namespace])
	}
	return buf.Bytes()
}

func signedSnapshotHash(snapshot *SignedSnapshot) [32]byte {
	return SigningHash(&wire.DKVSRecord{
		Version: Version,
		Key:     "/sys/snapshot/" + snapshot.Epoch,
		Value:   canonicalSignedSnapshotBytes(snapshot),
		PubKey:  []byte(snapshot.CreatedBy),
	})
}

func canonicalSignedSnapshotBytes(snapshot *SignedSnapshot) []byte {
	var buf bytes.Buffer
	writeString(&buf, snapshot.Epoch)
	writeUint64(&buf, snapshot.Height)
	writeUint64(&buf, snapshot.ActiveRecordCount)
	writeUint64(&buf, snapshot.ActiveRecordTotalSize)
	writeString(&buf, snapshot.ActiveRecordRoot)
	writeString(&buf, snapshot.SnapshotHash)
	writeUint64(&buf, snapshot.CreatedAt)
	writeString(&buf, snapshot.CreatedBy)
	namespaces := make([]string, 0, len(snapshot.NamespaceRoots))
	for namespace := range snapshot.NamespaceRoots {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	for _, namespace := range namespaces {
		writeString(&buf, namespace)
		writeString(&buf, snapshot.NamespaceRoots[namespace])
	}
	return buf.Bytes()
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
