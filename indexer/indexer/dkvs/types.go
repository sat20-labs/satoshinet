package dkvs

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	Version = uint32(1)

	FlagTombstone = uint32(1 << 0)

	EventRecordPut       = uint32(1)
	EventRecordUpdate    = uint32(2)
	EventRecordTombstone = uint32(3)
	EventPrefixUpdate    = uint32(4)
	EventMailboxMessage  = uint32(5)
	EventSyncHint        = uint32(6)
	EventCheckpointReady = uint32(7)
	EventSnapshotReady   = uint32(8)
	EventRenewal         = uint32(9)
	EventExpired         = uint32(10)

	MaxKeySize             = 256
	MaxKeySegmentSize      = 64
	MaxNamespaceSize       = 16
	MaxRecordValueSize     = wire.MaxDKVSRecordSize
	MaxFutureIssueTimeSkew = uint64(10 * 60 * 1000)
)

var (
	ErrInvalidRecord          = errors.New("invalid dkvs record")
	ErrInvalidKey             = errors.New("invalid dkvs key")
	ErrInvalidNamespace       = errors.New("invalid dkvs namespace")
	ErrInvalidSignature       = errors.New("invalid dkvs signature")
	ErrExpiredRecord          = errors.New("expired dkvs record")
	ErrRecordTooLarge         = errors.New("dkvs record too large")
	ErrPermissionDenied       = errors.New("dkvs permission denied")
	ErrDIDResolverUnavailable = errors.New("dkvs did resolver unavailable")
	ErrFeeProofRequired       = errors.New("dkvs fee proof required")
	ErrInvalidFeeProof        = errors.New("invalid dkvs fee proof")
	ErrFeeCapacityExceeded    = errors.New("dkvs fee capacity exceeded")
	ErrRecordNotFound         = errors.New("dkvs record not found")
	ErrInvalidCheckpoint      = errors.New("invalid dkvs checkpoint")
	ErrInvalidSnapshot        = errors.New("invalid dkvs snapshot")
	ErrMailboxFull            = errors.New("dkvs mailbox full")
	ErrBlobManifestInvalid    = errors.New("dkvs blob manifest invalid")
	ErrBlobChunkInvalid       = errors.New("dkvs blob chunk invalid")
	ErrTooManySubscriptions   = errors.New("too many dkvs subscriptions")
	ErrConcurrentUpdate       = errors.New("concurrent dkvs update")
)

type DIDIdentity struct {
	CanonicalName  string
	NameID         string
	SigningKeys    [][]byte
	OwnerAddresses []string
	AddressParams  *chaincfg.Params
	Active         bool
}

func (id DIDIdentity) CanSign(pubKey []byte) error {
	if !id.Active {
		return ErrPermissionDenied
	}
	for _, key := range id.SigningKeys {
		if bytes.Equal(key, pubKey) {
			return nil
		}
	}
	if len(id.OwnerAddresses) > 0 {
		addr, err := P2TRAddressFromPubKeyBytes(pubKey, id.AddressParams)
		if err != nil {
			return err
		}
		for _, owner := range id.OwnerAddresses {
			if owner == addr {
				return nil
			}
		}
	}
	return ErrPermissionDenied
}

type DIDResolver interface {
	ResolveName(name string) (DIDIdentity, error)
	ResolveService(serviceName string) (DIDIdentity, error)
}

type FeeVerifier interface {
	VerifyFeeProof(recordHash, keyHash [32]byte, namespace string, recordSize int, expiryHeight uint64, feeProof []byte) error
}

type RecordFeeVerifier interface {
	VerifyRecordFeeProof(record *wire.DKVSRecord, parsed ParsedKey) error
}

type FeeCapacityVerifier interface {
	VerifyFeeCapacity(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, records []*wire.DKVSRecord, height, now uint64) error
}

type FeeCapacityDescriptor struct {
	UsageKey   string
	MaxRecords uint64
}

type IndexedFeeCapacityVerifier interface {
	FeeCapacity(record *wire.DKVSRecord, parsed ParsedKey) (FeeCapacityDescriptor, error)
	FeeUsageKey(record *wire.DKVSRecord) (string, error)
}

type SystemVerifier interface {
	CanWriteSystem(key string, pubKey []byte) error
}

type NotifyFunc func(eventType uint32, key string, recordHash [32]byte, seq uint64, expiryHeight uint64, size uint32, flags uint32)

type SubscriptionNotifyFunc func(sub Subscription)

type Record = wire.DKVSRecord

type FeeProof struct {
	Mode          string `json:"mode"`
	PoolContract  string `json:"pool_contract,omitempty"`
	Payer         string `json:"payer,omitempty"`
	PaymentTxID   string `json:"payment_txid,omitempty"`
	LeaseContract string `json:"lease_contract,omitempty"`
	PlanID        string `json:"plan_id,omitempty"`
	PaidAmount    string `json:"paid_amount,omitempty"`
}

type NotifyEvent struct {
	EventType    uint32         `json:"event_type"`
	Key          string         `json:"key"`
	KeyHash      chainhash.Hash `json:"key_hash"`
	RecordHash   chainhash.Hash `json:"record_hash"`
	Seq          uint64         `json:"seq"`
	ExpiryHeight uint64         `json:"expiry_height"`
	Size         uint32         `json:"size"`
	SourceNode   string         `json:"source_node,omitempty"`
	Flags        uint32         `json:"flags"`
}

type Config struct {
	AllowFreeLocal bool
	Resolver       DIDResolver
	FeeVerifier    FeeVerifier
	SystemVerifier SystemVerifier
	Notify         NotifyFunc
	Subscription   SubscriptionNotifyFunc
	CurrentHeight  func() uint64
	SourceNode     string
	MailboxPolicy  MailboxPolicy
	BlobPolicy     BlobPolicy
	TmpPolicy      TmpPolicy
}

type SubscriptionType string

const (
	SubscriptionKey     SubscriptionType = "key"
	SubscriptionPrefix  SubscriptionType = "prefix"
	SubscriptionMailbox SubscriptionType = "mailbox"
	SubscriptionService SubscriptionType = "service"
)

type Subscription struct {
	Type   SubscriptionType `json:"type"`
	Target string           `json:"target"`
}

type Checkpoint struct {
	Height                uint64            `json:"height"`
	ActiveRecordCount     uint64            `json:"active_record_count"`
	ActiveRecordTotalSize uint64            `json:"active_record_total_size"`
	NamespaceRoots        map[string]string `json:"namespace_roots"`
	ActiveRecordRoot      string            `json:"active_record_root"`
}

type Usage struct {
	Prefix          string `json:"prefix"`
	ActiveRecords   uint64 `json:"active_records"`
	ActiveTotalSize uint64 `json:"active_total_size"`
}

// PathMeta is the compact aggregate maintained for a logical DKVS collection.
// It is stored once per collection path rather than once per record.
type PathMeta struct {
	Version         uint32         `json:"version"`
	Path            string         `json:"path"`
	Generation      uint64         `json:"generation"`
	ActiveRecords   uint64         `json:"active_records"`
	ActiveTotalSize uint64         `json:"active_total_size"`
	ActiveRoot      chainhash.Hash `json:"active_root"`
	MinExpiryHeight uint64         `json:"min_expiry_height,omitempty"`
	MinExpiryTime   uint64         `json:"min_expiry_time,omitempty"`
	UpdatedHeight   uint64         `json:"updated_height"`
	UpdatedAt       uint64         `json:"updated_at"`
	Dirty           bool           `json:"dirty,omitempty"`
}

type Snapshot struct {
	Checkpoint *Checkpoint        `json:"checkpoint"`
	Records    []*wire.DKVSRecord `json:"records"`
	CreatedAt  uint64             `json:"created_at"`
}

type MailboxPolicy struct {
	MaxMsgBytes   uint64
	MaxMessages   uint64
	MaxMsgSize    int
	MaxMsgTTL     uint64
	MaxShareBytes uint64
	MaxShares     uint64
	MaxShareSize  int
	MaxShareTTL   uint64
}

type BlobPolicy struct {
	MaxTotalSize uint64
	MaxChunkSize int
	MaxChunks    uint32
}

type TmpPolicy struct {
	MaxTTL  uint64
	MaxSize int
}

type BlobManifest struct {
	ContentHash  string          `json:"content_hash"`
	TotalSize    uint64          `json:"total_size"`
	ChunkSize    uint32          `json:"chunk_size"`
	ChunkCount   uint32          `json:"chunk_count"`
	ChunkHashes  []string        `json:"chunk_hashes"`
	TTL          uint64          `json:"ttl,omitempty"`
	ExpiryHeight uint64          `json:"expiry_height,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

type defaultResolver struct{}

func (defaultResolver) ResolveName(string) (DIDIdentity, error) {
	return DIDIdentity{}, ErrDIDResolverUnavailable
}

func (defaultResolver) ResolveService(string) (DIDIdentity, error) {
	return DIDIdentity{}, ErrDIDResolverUnavailable
}

type defaultFeeVerifier struct {
	allowFreeLocal bool
}

func (v defaultFeeVerifier) VerifyFeeProof(_, _ [32]byte, _ string, _ int, _ uint64, feeProof []byte) error {
	if v.allowFreeLocal && len(feeProof) == 0 {
		return nil
	}
	return ErrFeeProofRequired
}

type defaultSystemVerifier struct{}

func (defaultSystemVerifier) CanWriteSystem(_ string, _ []byte) error {
	return ErrPermissionDenied
}
