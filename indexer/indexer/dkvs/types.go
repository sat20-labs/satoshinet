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
	// This flag identifies a transient signed delete command. Such commands
	// are not stored as records and never occur in a current-state snapshot.
	FlagTombstone        = uint32(1 << 0)
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
	MaxKeySize           = 256
	MaxKeySegmentSize    = 64
	MaxNamespaceSize     = 16
	MaxRecordValueSize   = wire.MaxDKVSValueSize
	// Wall-clock duration depends on actual block cadence.
	DefaultFreeLocalMaxTTLBlocks = uint64(24 * 60 * 60 / 12)
	MaxBatchCASMutations         = 64
	MaxBatchCASTotalSize         = 8 * 1024 * 1024
	MaxPrefixesPerTerminal       = 16
	MaxPrefixLength              = 256
	MaxPrefixReadRecords         = 4096
	MaxPrefixReadBytes           = 16 * 1024 * 1024
)

type ErrorCode string

const (
	ErrorCodeWriteConflict             ErrorCode = "DKVS_WRITE_CONFLICT"
	ErrorCodeStaleGeneration           ErrorCode = "DKVS_STALE_GENERATION"
	ErrorCodeStaleEndpoint             ErrorCode = "DKVS_STALE_ENDPOINT"
	ErrorCodeEndpointMismatch          ErrorCode = "DKVS_ENDPOINT_MISMATCH"
	ErrorCodeResetRequired             ErrorCode = "DKVS_RESET_REQUIRED"
	ErrorCodePermissionDenied          ErrorCode = "DKVS_PERMISSION_DENIED"
	ErrorCodeInvalidSequence           ErrorCode = "DKVS_INVALID_SEQUENCE"
	ErrorCodePathDiverged              ErrorCode = "DKVS_PATH_DIVERGED"
	ErrorCodeLocalOnlyEndpointMismatch ErrorCode = "DKVS_LOCAL_ONLY_ENDPOINT_MISMATCH"
	ErrorCodeStorageModeDowngrade      ErrorCode = "DKVS_STORAGE_MODE_DOWNGRADE"
	ErrorCodeQuotaExceeded             ErrorCode = "DKVS_QUOTA_EXCEEDED"
	ErrorCodeInvalidRecord             ErrorCode = "DKVS_INVALID_RECORD"
	ErrorCodeExpiredRecord             ErrorCode = "DKVS_EXPIRED_RECORD"
	ErrorCodeRecordNotFound            ErrorCode = "DKVS_RECORD_NOT_FOUND"
)

var (
	ErrInvalidRecord             = errors.New("invalid dkvs record")
	ErrInvalidKey                = errors.New("invalid dkvs key")
	ErrInvalidNamespace          = errors.New("invalid dkvs namespace")
	ErrInvalidSignature          = errors.New("invalid dkvs signature")
	ErrExpiredRecord             = errors.New("expired dkvs record")
	ErrRecordTooLarge            = errors.New("dkvs record too large")
	ErrPermissionDenied          = errors.New("dkvs permission denied")
	ErrDIDResolverUnavailable    = errors.New("dkvs did resolver unavailable")
	ErrFeeProofRequired          = errors.New("dkvs fee proof required")
	ErrInvalidFeeProof           = errors.New("invalid dkvs fee proof")
	ErrFeeCapacityExceeded       = errors.New("dkvs fee capacity exceeded")
	ErrRecordNotFound            = errors.New("dkvs record not found")
	ErrInvalidCheckpoint         = errors.New("invalid dkvs checkpoint")
	ErrInvalidSnapshot           = errors.New("invalid dkvs snapshot")
	ErrMailboxFull               = errors.New("dkvs mailbox full")
	ErrTooManySubscriptions      = errors.New("too many dkvs subscriptions")
	ErrConcurrentUpdate          = errors.New("concurrent dkvs update")
	ErrWriteConflict             = errors.New("dkvs write conflict")
	ErrStaleGeneration           = errors.New("dkvs stale generation")
	ErrStaleEndpoint             = errors.New("dkvs stale endpoint")
	ErrEndpointMismatch          = errors.New("dkvs endpoint mismatch")
	ErrResetRequired             = errors.New("dkvs subscription reset required")
	ErrInvalidSequence           = errors.New("dkvs invalid sequence")
	ErrPathDiverged              = errors.New("dkvs path diverged")
	ErrPathGenerationGap         = errors.New("dkvs path generation gap")
	ErrLocalOnlyEndpointMismatch = errors.New("dkvs local-only endpoint mismatch")
	ErrStorageModeDowngrade      = errors.New("dkvs paid record cannot downgrade to free local")
	ErrBatchTooLarge             = errors.New("dkvs batch too large")
	ErrFreeLocalDisabled         = errors.New("dkvs free local cache is disabled")
	ErrFreeLocalQuotaExceeded    = errors.New("dkvs free local cache quota exceeded")
	ErrFreeLocalNotRelayable     = errors.New("dkvs free local record is not relayable")
)

// Callers branch on the API code or errors.Is/As, not human-readable text.
func ErrorCodeOf(err error) ErrorCode {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrEndpointMismatch):
		return ErrorCodeEndpointMismatch
	case errors.Is(err, ErrResetRequired):
		return ErrorCodeResetRequired
	case errors.Is(err, ErrStaleEndpoint):
		return ErrorCodeStaleEndpoint
	case errors.Is(err, ErrStaleGeneration), errors.Is(err, ErrPathGenerationGap):
		return ErrorCodeStaleGeneration
	case errors.Is(err, ErrInvalidSequence):
		return ErrorCodeInvalidSequence
	case errors.Is(err, ErrPathDiverged):
		return ErrorCodePathDiverged
	case errors.Is(err, ErrLocalOnlyEndpointMismatch):
		return ErrorCodeLocalOnlyEndpointMismatch
	case errors.Is(err, ErrStorageModeDowngrade):
		return ErrorCodeStorageModeDowngrade
	case errors.Is(err, ErrPermissionDenied), errors.Is(err, ErrDIDResolverUnavailable):
		return ErrorCodePermissionDenied
	case errors.Is(err, ErrFeeCapacityExceeded), errors.Is(err, ErrMailboxFull), errors.Is(err, ErrFreeLocalQuotaExceeded):
		return ErrorCodeQuotaExceeded
	case errors.Is(err, ErrWriteConflict), errors.Is(err, ErrConcurrentUpdate):
		return ErrorCodeWriteConflict
	case errors.Is(err, ErrRecordNotFound):
		return ErrorCodeRecordNotFound
	case errors.Is(err, ErrExpiredRecord):
		return ErrorCodeExpiredRecord
	default:
		return ErrorCodeInvalidRecord
	}
}

type PathMode uint8

const (
	PathOwnerExclusive PathMode = iota
	PathAuthorityExclusive
	PathSharedAppend
	PathLocalOnly
)

func (mode PathMode) String() string {
	switch mode {
	case PathOwnerExclusive:
		return "owner_exclusive"
	case PathAuthorityExclusive:
		return "authority_exclusive"
	case PathSharedAppend:
		return "shared_append"
	case PathLocalOnly:
		return "local_only"
	default:
		return "unknown"
	}
}

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
	// Local delivery metadata, not serialized into an untrusted peer record.
	Relay bool `json:"-"`
}

type FreeLocalCachePolicy struct {
	Enabled             bool   `json:"enabled"`
	MaxTTL              uint64 `json:"max_ttl_blocks"`
	MaxRecordsPerSigner uint64 `json:"max_records_per_signer"`
	MaxBytesPerSigner   uint64 `json:"max_bytes_per_signer"`
	MaxTotalRecords     uint64 `json:"max_total_records"`
	MaxTotalBytes       uint64 `json:"max_total_bytes"`
}

func DefaultFreeLocalCachePolicy() FreeLocalCachePolicy {
	return FreeLocalCachePolicy{Enabled: true, MaxTTL: DefaultFreeLocalMaxTTLBlocks,
		MaxRecordsPerSigner: 100, MaxBytesPerSigner: 1 << 20, MaxTotalRecords: 100000, MaxTotalBytes: 1 << 30}
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
	EndpointID     string
}

type WritePrecondition struct {
	ExpectedHash *chainhash.Hash `json:"expected_etag,omitempty"`
	ExpectAbsent bool            `json:"expect_absent,omitempty"`
}

func (p WritePrecondition) Valid() bool { return p.ExpectAbsent != (p.ExpectedHash != nil) }

type CASMutation struct {
	Record       *wire.DKVSRecord  `json:"record"`
	Precondition WritePrecondition `json:"precondition"`
}
type BatchCASOptions struct {
	EndpointID string `json:"endpoint_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}
type WriteResult struct {
	Applied      int                `json:"applied"`
	Records      []*wire.DKVSRecord `json:"records,omitempty"`
	Hashes       []string           `json:"etags,omitempty"`
	PrefixStates []PrefixGeneration `json:"prefix_states,omitempty"`
	ViewHeight   uint64             `json:"view_height"`
	ServerTimeMS uint64             `json:"server_time_ms"`
	LocalOnly    bool               `json:"local_only,omitempty"`
	EndpointID   string             `json:"endpoint_id,omitempty"`
	RequestID    string             `json:"request_id,omitempty"`
}

type StorageMode string

const (
	StorageModeFreeLocal StorageMode = "FREE_LOCAL"
	StorageModeAutopay   StorageMode = "AUTOPAY"
	StorageModePaid      StorageMode = "PAID"
)

type KeyStateStatus string

const (
	KeyStateActive    KeyStateStatus = "active"
	KeyStateDeleted   KeyStateStatus = "deleted"
	KeyStateNeverSeen KeyStateStatus = "never_seen"
)

type DKVSKeyState struct {
	Key          string           `json:"key"`
	Status       KeyStateStatus   `json:"status"`
	Seq          uint64           `json:"seq,omitempty"`
	ETag         string           `json:"etag,omitempty"`
	ExpiryHeight uint64           `json:"expiry_height,omitempty"`
	StorageMode  StorageMode      `json:"storage_mode,omitempty"`
	Record       *wire.DKVSRecord `json:"record,omitempty"`
}

// Positions belong to one source endpoint and scope, never to a global clock.
type PrefixGeneration struct {
	Prefix     string `json:"prefix"`
	Generation uint64 `json:"generation"`
}
type PrefixReadResult struct {
	EndpointID string             `json:"endpoint_id"`
	Prefix     string             `json:"prefix"`
	ViewHeight uint64             `json:"view_height"`
	Records    []*wire.DKVSRecord `json:"records"`
	KeyStates  []DKVSKeyState     `json:"key_states,omitempty"`
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

// Both counters are local to this node. Network state excludes endpoint-local
// records; EndpointGeneration also observes FREE_LOCAL and bound mailbox data.
type PathMeta struct {
	Version            uint32         `json:"version"`
	Path               string         `json:"path"`
	Generation         uint64         `json:"generation"`
	EndpointGeneration uint64         `json:"endpoint_generation"`
	StateRoot          chainhash.Hash `json:"state_root"`
	ActiveRecords      uint64         `json:"active_records"`
	ActiveTotalSize    uint64         `json:"active_total_size"`
	MinExpiryHeight    uint64         `json:"min_expiry_height,omitempty"`
	ViewHeight         uint64         `json:"view_height"`
	ActiveRoot         chainhash.Hash `json:"-"`
	MinExpiryTime      uint64         `json:"-"`
	UpdatedHeight      uint64         `json:"-"`
	UpdatedAt          uint64         `json:"-"`
	Dirty              bool           `json:"-"`
}
type PathLocalStatus struct {
	Path            string `json:"path"`
	UpdatedAt       uint64 `json:"updated_at,omitempty"`
	LastSyncAt      uint64 `json:"last_sync_at,omitempty"`
	LastSyncPeer    string `json:"last_sync_peer,omitempty"`
	Dirty           bool   `json:"dirty,omitempty"`
	Stale           bool   `json:"stale,omitempty"`
	LocalRetryState string `json:"local_retry_state,omitempty"`
}

// No per-key deletion history is part of a complete current network view.
type PathSnapshot struct {
	Path         string             `json:"path"`
	PathMeta     *PathMeta          `json:"pathmeta"`
	Records      []*wire.DKVSRecord `json:"records"`
	ServerTimeMS uint64             `json:"server_time_ms"`
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
type ClientConfig struct {
	FreeLocal              FreeLocalCachePolicy `json:"free_local"`
	Blob                   BlobPolicy           `json:"blob"`
	MaxBatchMutations      int                  `json:"max_batch_mutations"`
	MaxBatchBytes          int                  `json:"max_batch_record_bytes"`
	MaxPrefixesPerTerminal int                  `json:"max_prefixes_per_terminal"`
	EndpointID             string               `json:"endpoint_id,omitempty"`
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

func (defaultSystemVerifier) CanWriteSystem(_ string, _ []byte) error { return ErrPermissionDenied }
