package dkvs

import (
	"errors"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	Version = uint32(1)

	FlagTombstone = uint32(1 << 0)

	EventRecordPut       = uint32(1)
	EventRecordUpdate    = uint32(2)
	EventRecordTombstone = uint32(3)
	EventSyncHint        = uint32(6)

	MaxKeySize         = 256
	MaxKeySegmentSize  = 64
	MaxNamespaceSize   = 16
	MaxRecordValueSize = 10 * 1024
	MaxRecordDataSize  = 10 * 1024
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
	ErrRecordNotFound         = errors.New("dkvs record not found")
)

type DIDResolver interface {
	CanWriteName(name string, pubKey []byte) error
	CanWriteService(serviceName string, pubKey []byte) error
}

type FeeVerifier interface {
	VerifyFeeProof(recordHash [32]byte, namespace string, recordSize int, feeProof []byte) error
}

type NotifyFunc func(eventType uint32, key string, recordHash [32]byte, seq uint64, expiryHeight uint64, size uint32, flags uint32)

type Record = wire.DKVSRecord

type FeeProof struct {
	Mode           string         `json:"mode"`
	PoolContract   string         `json:"pool_contract,omitempty"`
	Payer          string         `json:"payer,omitempty"`
	PaymentTxID    string         `json:"payment_txid,omitempty"`
	LeaseContract  string         `json:"lease_contract,omitempty"`
	PlanID         string         `json:"plan_id,omitempty"`
	KeyHash        chainhash.Hash `json:"key_hash"`
	RecordHash     chainhash.Hash `json:"record_hash"`
	RecordSize     uint32         `json:"record_size"`
	ExpiryHeight   uint64         `json:"expiry_height"`
	Namespace      string         `json:"namespace"`
	PaidAmount     string         `json:"paid_amount,omitempty"`
	ProofSignature []byte         `json:"proof_signature,omitempty"`
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
	Notify         NotifyFunc
	CurrentHeight  func() uint64
	SourceNode     string
}

type Checkpoint struct {
	Height                uint64            `json:"height"`
	ActiveRecordCount     uint64            `json:"active_record_count"`
	ActiveRecordTotalSize uint64            `json:"active_record_total_size"`
	NamespaceRoots        map[string]string `json:"namespace_roots"`
	ActiveRecordRoot      string            `json:"active_record_root"`
}

type defaultResolver struct{}

func (defaultResolver) CanWriteName(string, []byte) error {
	return ErrDIDResolverUnavailable
}

func (defaultResolver) CanWriteService(string, []byte) error {
	return ErrDIDResolverUnavailable
}

type defaultFeeVerifier struct {
	allowFreeLocal bool
}

func (v defaultFeeVerifier) VerifyFeeProof(_ [32]byte, _ string, _ int, feeProof []byte) error {
	if len(feeProof) == 0 && !v.allowFreeLocal {
		return ErrFeeProofRequired
	}
	return nil
}
