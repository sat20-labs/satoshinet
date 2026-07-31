package dkvs

import (
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type RecordOptions struct {
	Seq            uint64
	PathGeneration uint64
	IssueTime      uint64
	TTL            uint64
	ExpiryHeight   uint64
	FeeProof       []byte
	Flags          uint32
}

type RecordVerificationOptions struct {
	ExpectedKey  string
	ExpectedHash chainhash.Hash
	CheckHash    bool
	Height       uint64
	Now          uint64
	FeeVerifier  FeeVerifier
}

func NewRecord(key string, value []byte, pubKey []byte, opts RecordOptions) (*wire.DKVSRecord, error) {
	parsed, err := ParseKey(key)
	if err != nil {
		return nil, err
	}
	record := &wire.DKVSRecord{
		Version:        Version,
		Key:            key,
		Value:          append([]byte{}, value...),
		PubKey:         append([]byte{}, pubKey...),
		Seq:            opts.Seq,
		PathGeneration: opts.PathGeneration,
		IssueTime:      opts.IssueTime,
		TTL:            opts.TTL,
		ExpiryHeight:   opts.ExpiryHeight,
		FeeProof:       append([]byte{}, opts.FeeProof...),
		Flags:          opts.Flags,
	}
	if record.IssueTime == 0 {
		record.IssueTime = currentUnixMilli()
	}
	if err := validateRecordSizeForParsed(record, parsed); err != nil {
		return nil, err
	}
	return record, nil
}

func VerifyRecordForClient(record *wire.DKVSRecord, opts RecordVerificationOptions) error {
	if record == nil || record.Version != Version {
		return ErrInvalidRecord
	}
	if opts.ExpectedKey != "" && record.Key != opts.ExpectedKey {
		return ErrInvalidKey
	}
	parsed, err := validateParsedCoreWithVerifier(record, opts.Height, opts.Now, true, false, nil)
	if err != nil {
		return err
	}
	if IsBlobKey(parsed) {
		if err := validateBlobRecord(record, parsed, DefaultBlobPolicy()); err != nil {
			return err
		}
	}
	if opts.CheckHash && RecordHash(record) != opts.ExpectedHash {
		return ErrInvalidRecord
	}
	if opts.FeeVerifier != nil && !IsTombstone(record.Flags) {
		if err := verifyFeeProofWith(opts.FeeVerifier, record, parsed); err != nil {
			return err
		}
	}
	return nil
}

func VerifyRecordsForClient(records []*wire.DKVSRecord, prefix string, opts RecordVerificationOptions) error {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if prefix != "" {
		if _, err := ParsePrefix(prefix); err != nil {
			return err
		}
	}
	if opts.CheckHash && len(records) != 1 {
		return ErrInvalidRecord
	}
	for _, record := range records {
		if prefix != "" && (record == nil || (record.Key != prefix && !strings.HasPrefix(record.Key, prefix+"/"))) {
			return ErrInvalidKey
		}
		recordOpts := opts
		if prefix != "" {
			recordOpts.ExpectedKey = ""
		}
		if err := VerifyRecordForClient(record, recordOpts); err != nil {
			return err
		}
	}
	return nil
}

func VerifySubscriptionRecordsForClient(records []*wire.DKVSRecord, sub Subscription, opts RecordVerificationOptions) error {
	sub, err := validateSubscription(sub)
	if err != nil {
		return err
	}
	if opts.CheckHash && len(records) != 1 {
		return ErrInvalidRecord
	}
	for _, record := range records {
		if record == nil || !subscriptionMatchesKey(sub, record.Key) {
			return ErrInvalidKey
		}
		recordOpts := opts
		recordOpts.ExpectedKey = ""
		if err := VerifyRecordForClient(record, recordOpts); err != nil {
			return err
		}
	}
	return nil
}

func AccountID(pubKey []byte) string {
	accountID, err := CanonicalAccountID(pubKey)
	if err != nil {
		return ""
	}
	return accountID
}

func PersonalKey(pubKey []byte, path string) (string, error) {
	accountID, err := CanonicalAccountID(pubKey)
	if err != nil {
		return "", err
	}
	return AccountPersonalKey(accountID, path)
}

func NameKey(name string) (string, error) {
	key := "/name/" + NormalizeNameID(name)
	_, err := ParseKey(key)
	return key, err
}

func ServiceKey(serviceName, path string) (string, error) {
	key := "/svc/" + NormalizeNameID(serviceName) + "/" + normalizePath(path)
	_, err := ParseKey(key)
	return key, err
}

func MailMsgKey(mailboxID, senderID, msgID string) (string, error) {
	key := "/mail/" + mailboxID + "/msg/" + senderID + "/" + msgID
	_, err := ParseKey(key)
	return key, err
}

func MailShareKey(mailboxID, packageID, shareID string) (string, error) {
	key := "/mail/" + mailboxID + "/share/" + packageID + "/" + shareID
	_, err := ParseKey(key)
	return key, err
}

func BlobKey(accountID, blobName string) (string, error) {
	accountID = strings.ToLower(strings.TrimSpace(accountID))
	blobName = strings.TrimSpace(blobName)
	key := "/blob/" + accountID + "/" + blobName
	_, err := ParseKey(key)
	return key, err
}

func TmpKey(randomID string) (string, error) {
	key := "/tmp/" + strings.TrimSpace(randomID)
	_, err := ParseKey(key)
	return key, err
}

func DirectoryRootFromRecords(records []*wire.DKVSRecord, height uint64) (chainhash.Hash, error) {
	return recordsRoot(records, height)
}

func CheckpointFromRecords(records []*wire.DKVSRecord, height uint64) (*Checkpoint, error) {
	return checkpointFromRecords(records, height)
}

func ValidateSnapshot(snapshot *Snapshot) error {
	if snapshot == nil || snapshot.Checkpoint == nil {
		return ErrInvalidSnapshot
	}
	checkpoint, err := checkpointFromRecords(snapshot.Records, snapshot.Checkpoint.Height)
	if err != nil {
		return err
	}
	if checkpoint.ActiveRecordRoot != snapshot.Checkpoint.ActiveRecordRoot ||
		checkpoint.ActiveRecordCount != snapshot.Checkpoint.ActiveRecordCount ||
		checkpoint.ActiveRecordTotalSize != snapshot.Checkpoint.ActiveRecordTotalSize {
		return ErrInvalidSnapshot
	}
	if len(checkpoint.NamespaceRoots) != len(snapshot.Checkpoint.NamespaceRoots) {
		return ErrInvalidSnapshot
	}
	for namespace, root := range checkpoint.NamespaceRoots {
		if snapshot.Checkpoint.NamespaceRoots[namespace] != root {
			return ErrInvalidSnapshot
		}
	}
	return nil
}

func normalizePath(path string) string {
	return strings.Trim(strings.TrimSpace(path), "/")
}
