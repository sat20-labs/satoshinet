package dkvs

import (
	"bytes"
	"errors"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	Version = uint32(1)

	FlagTombstone = uint32(1 << 0)

	EventRecordPut       = uint8(1)
	EventRecordUpdate    = uint8(2)
	EventRecordTombstone = uint8(3)
	EventPrefixUpdate    = uint8(4)
	EventMailboxMessage  = uint8(5)
	EventSyncHint        = uint8(6)
	EventCheckpointReady = uint8(7)
	EventSnapshotReady   = uint8(8)
	EventRenewal         = uint8(9)
	EventExpired         = uint8(10)

	MaxKeySize             = 256
	MaxKeySegmentSize      = 64
	MaxNamespaceSize       = 16
	MaxRecordValueSize     = wire.MaxDKVSValueSize
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
	ErrTooManySubscriptions   = errors.New("too many dkvs subscriptions")
	ErrConcurrentUpdate       = errors.New("concurrent dkvs update")
	ErrWriteConflict          = errors.New("dkvs write conflict")
	ErrBatchTooLarge          = errors.New("dkvs batch too large")
	ErrFreeLocalDisabled      = errors.New("dkvs free local cache is disabled")
	ErrFreeLocalQuotaExceeded = errors.New("dkvs free local cache quota exceeded")
	ErrFreeLocalNotRelayable  = errors.New("dkvs free local record is not relayable")
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

type NotifyFunc func(event *NotifyEvent)

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
	EventType uint8  `json:"event_type"`
	Data      []byte `json:"data"`
	// Relay is local delivery metadata. It is deliberately not serialized into
	// MsgDKVSNotify: a peer must independently validate every received record.
	Relay bool `json:"-"`
}

// FreeLocalCachePolicy bounds records that have no paid fee proof. Those
// records are admitted by one node only and are never relayed through DKVS P2P.
type FreeLocalCachePolicy struct {
	Enabled             bool   `json:"enabled"`
	MaxTTL              uint64 `json:"max_ttl_ms"`
	MaxRecordsPerSigner uint64 `json:"max_records_per_signer"`
	MaxBytesPerSigner   uint64 `json:"max_bytes_per_signer"`
	MaxTotalRecords     uint64 `json:"max_total_records"`
	MaxTotalBytes       uint64 `json:"max_total_bytes"`
}

// DefaultFreeLocalCachePolicy is intentionally centralized so operators can
// tune one default before it is exposed to connected wallets.
func DefaultFreeLocalCachePolicy() FreeLocalCachePolicy {
	return FreeLocalCachePolicy{
		Enabled:             true,
		MaxTTL:              24 * 60 * 60 * 1000,
		MaxRecordsPerSigner: 100,
		MaxBytesPerSigner:   1 << 20,
		MaxTotalRecords:     100000,
		MaxTotalBytes:       1 << 30,
	}
}

type Config struct {
	AllowFreeLocal bool
	FreeLocalCache FreeLocalCachePolicy
	Resolver       DIDResolver
	FeeVerifier    FeeVerifier
	SystemVerifier SystemVerifier
	Notify         NotifyFunc
	Subscription   SubscriptionNotifyFunc
	CurrentHeight  func() uint64
	MailboxPolicy  MailboxPolicy
	BlobPolicy     BlobPolicy
	TmpPolicy      TmpPolicy
}

const (
	MaxBatchCASMutations = 64
	MaxBatchCASTotalSize = 8 * 1024 * 1024
)

type WritePrecondition struct {
	ExpectedHash *chainhash.Hash `json:"expected_hash,omitempty"`
	ExpectAbsent bool            `json:"expect_absent,omitempty"`
}

func (p WritePrecondition) Valid() bool {
	return p.ExpectAbsent != (p.ExpectedHash != nil)
}

type CASMutation struct {
	Record       *wire.DKVSRecord  `json:"record"`
	Precondition WritePrecondition `json:"precondition"`
}

// PathWritePrecondition proves that the caller built a mutation from a
// complete, current view of a logical DKVS collection.
type PathWritePrecondition struct {
	Path               string         `json:"path"`
	ExpectedRoot       chainhash.Hash `json:"expected_root"`
	ExpectedGeneration uint64         `json:"expected_generation"`
}

type BatchCASOptions struct {
	PathPreconditions []PathWritePrecondition `json:"path_preconditions,omitempty"`
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
	MaxMsgBytes          uint64
	MaxMessages          uint64
	MaxMsgBytesPerSender uint64
	MaxMessagesPerSender uint64
	MaxMsgSize           int
	MaxMsgTTL            uint64
	MaxShareBytes        uint64
	MaxShares            uint64
	MaxShareSize         int
	MaxShareTTL          uint64
}

type BlobPolicy struct {
	MaxValueSize              int    `json:"max_value_size"`
	MaxFreeLocalKeysPerSigner uint64 `json:"max_free_local_keys_per_signer"`
}

// ClientConfig is the node policy exposed to wallet SDKs and applications.
type ClientConfig struct {
	FreeLocal         FreeLocalCachePolicy `json:"free_local"`
	Blob              BlobPolicy           `json:"blob"`
	MaxBatchMutations int                  `json:"max_batch_mutations"`
	MaxBatchBytes     int                  `json:"max_batch_record_bytes"`
}

type TmpPolicy struct {
	MaxTTL  uint64
	MaxSize int
}

type defaultResolver struct{}

func (defaultResolver) ResolveName(string) (DIDIdentity, error) {
	return DIDIdentity{}, ErrDIDResolverUnavailable
}

func (defaultResolver) ResolveService(string) (DIDIdentity, error) {
	return DIDIdentity{}, ErrDIDResolverUnavailable
}

type defaultFeeVerifier struct {
	allowFreeLocal     bool
	allowEmptyFeeProof bool
}

func (v defaultFeeVerifier) VerifyFeeProof(_, _ [32]byte, _ string, _ int, _ uint64, feeProof []byte) error {
	if v.allowFreeLocal && len(feeProof) == 0 && v.allowEmptyFeeProof {
		return nil
	}
	if v.allowFreeLocal && len(feeProof) != 0 {
		proof, err := ParseFeeProof(feeProof)
		if err == nil && proof.Mode == FeeModeFreeLocal {
			return nil
		}
	}
	return ErrFeeProofRequired
}

type defaultSystemVerifier struct{}

func (defaultSystemVerifier) CanWriteSystem(_ string, _ []byte) error {
	return ErrPermissionDenied
}
