package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	FeeModeOneshot   = "ONESHOT"
	FeeModeLease     = "LEASE"
	FeeModeAutopay   = "AUTOPAY"
	FeeModeFreeLocal = "FREE_LOCAL"
)

type JSONFeeVerifier struct {
	AllowFreeLocal         bool
	AllowMissingRecordHash bool
	RequireProofSignature  bool
}

type HTTPFeeVerifier struct {
	Endpoint string
	Client   *http.Client
}

func NewOneshotFeeProof(key, namespace string, recordSize uint32, expiryHeight uint64, poolContract, payer, paymentTxID, paidAmount string) (*FeeProof, error) {
	parsed, err := parseFeeProofKeyNamespace(key, namespace)
	if err != nil {
		return nil, err
	}
	poolContract = strings.TrimSpace(poolContract)
	payer = strings.TrimSpace(payer)
	paymentTxID = strings.TrimSpace(paymentTxID)
	if poolContract == "" || payer == "" || paymentTxID == "" {
		return nil, ErrInvalidFeeProof
	}
	return &FeeProof{
		Mode:         FeeModeOneshot,
		PoolContract: poolContract,
		Payer:        payer,
		PaymentTxID:  paymentTxID,
		KeyHash:      KeyHash(key),
		RecordSize:   recordSize,
		ExpiryHeight: expiryHeight,
		Namespace:    parsed.Namespace,
		PaidAmount:   paidAmount,
	}, nil
}

func NewLeaseFeeProof(key, namespace string, recordSize uint32, expiryHeight uint64, poolContract, leaseContract, planID string) (*FeeProof, error) {
	parsed, err := parseFeeProofKeyNamespace(key, namespace)
	if err != nil {
		return nil, err
	}
	poolContract = strings.TrimSpace(poolContract)
	leaseContract = strings.TrimSpace(leaseContract)
	planID = strings.TrimSpace(planID)
	if poolContract == "" || leaseContract == "" || planID == "" {
		return nil, ErrInvalidFeeProof
	}
	return &FeeProof{
		Mode:          FeeModeLease,
		PoolContract:  poolContract,
		LeaseContract: leaseContract,
		PlanID:        planID,
		KeyHash:       KeyHash(key),
		RecordSize:    recordSize,
		ExpiryHeight:  expiryHeight,
		Namespace:     parsed.Namespace,
	}, nil
}

func NewAutopayFeeProof(key, namespace string, recordSize uint32, expiryHeight uint64, poolContract, payer string) (*FeeProof, error) {
	parsed, err := parseFeeProofKeyNamespace(key, namespace)
	if err != nil {
		return nil, err
	}
	poolContract = strings.TrimSpace(poolContract)
	payer = strings.TrimSpace(payer)
	if poolContract == "" || payer == "" {
		return nil, ErrInvalidFeeProof
	}
	if recordSize < wire.MaxDKVSRecordSize {
		recordSize = wire.MaxDKVSRecordSize
	}
	return &FeeProof{
		Mode:         FeeModeAutopay,
		PoolContract: poolContract,
		Payer:        payer,
		KeyHash:      KeyHash(key),
		RecordSize:   recordSize,
		ExpiryHeight: expiryHeight,
		Namespace:    parsed.Namespace,
	}, nil
}

func NewFreeLocalFeeProof(key, namespace string, recordSize uint32, expiryHeight uint64) (*FeeProof, error) {
	parsed, err := parseFeeProofKeyNamespace(key, namespace)
	if err != nil {
		return nil, err
	}
	return &FeeProof{
		Mode:         FeeModeFreeLocal,
		KeyHash:      KeyHash(key),
		RecordSize:   recordSize,
		ExpiryHeight: expiryHeight,
		Namespace:    parsed.Namespace,
	}, nil
}

func parseFeeProofKeyNamespace(key, namespace string) (ParsedKey, error) {
	parsed, err := ParseKey(key)
	if err != nil {
		return parsed, err
	}
	if strings.TrimSpace(namespace) != parsed.Namespace {
		return parsed, ErrInvalidNamespace
	}
	return parsed, nil
}

func EncodeFeeProof(proof *FeeProof) ([]byte, error) {
	if proof == nil {
		return nil, ErrInvalidFeeProof
	}
	proofCopy := *proof
	proofCopy.Mode = strings.ToUpper(strings.TrimSpace(proofCopy.Mode))
	encoded, err := json.Marshal(proofCopy)
	if err != nil {
		return nil, err
	}
	if _, err := ParseFeeProof(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

var feeProofSignatureDomain = []byte("satoshinet-dkvs-fee-proof-v1")

func VerifyFeeProofSignature(proof *FeeProof) error {
	if proof == nil || len(proof.ProofSignature) == 0 {
		return ErrInvalidFeeProof
	}
	pubKeyBytes := proof.PayerPubKey
	if len(pubKeyBytes) == 0 && proof.Payer != "" {
		decoded, err := hex.DecodeString(proof.Payer)
		if err != nil {
			return ErrInvalidFeeProof
		}
		pubKeyBytes = decoded
	}
	pubKey, err := btcec.ParsePubKey(pubKeyBytes)
	if err != nil {
		return ErrInvalidFeeProof
	}
	sig, err := ecdsa.ParseSignature(proof.ProofSignature)
	if err != nil {
		return ErrInvalidFeeProof
	}
	hash := FeeProofSigningHash(proof)
	if !sig.Verify(hash[:], pubKey) {
		return ErrInvalidFeeProof
	}
	return nil
}

func FeeProofSigningHash(proof *FeeProof) [32]byte {
	return sha256.Sum256(canonicalFeeProofBytes(proof))
}

func canonicalFeeProofBytes(proof *FeeProof) []byte {
	var buf bytes.Buffer
	writeBytes(&buf, feeProofSignatureDomain)
	if proof == nil {
		return buf.Bytes()
	}
	writeString(&buf, strings.ToUpper(strings.TrimSpace(proof.Mode)))
	writeString(&buf, proof.PoolContract)
	writeString(&buf, proof.Payer)
	writeBytes(&buf, proof.PayerPubKey)
	writeString(&buf, proof.PaymentTxID)
	writeString(&buf, proof.LeaseContract)
	writeString(&buf, proof.PlanID)
	writeBytes(&buf, proof.KeyHash[:])
	writeBytes(&buf, proof.RecordHash[:])
	writeUint32(&buf, proof.RecordSize)
	writeUint64(&buf, proof.ExpiryHeight)
	writeString(&buf, proof.Namespace)
	writeString(&buf, proof.PaidAmount)
	return buf.Bytes()
}

func FeeAnchorHash(record *wire.DKVSRecord) chainhash.Hash {
	if record == nil {
		return chainhash.Hash{}
	}
	recordCopy := *record
	recordCopy.Signature = nil
	if len(record.FeeProof) != 0 {
		if proof, err := ParseFeeProof(record.FeeProof); err == nil {
			proof.RecordHash = chainhash.Hash{}
			proof.ProofSignature = nil
			if encoded, err := json.Marshal(proof); err == nil {
				recordCopy.FeeProof = encoded
			}
		}
	}
	return chainhash.DoubleHashH(canonicalRecordBytes(&recordCopy, false))
}

func ParseFeeProof(data []byte) (*FeeProof, error) {
	if len(data) == 0 {
		return nil, ErrFeeProofRequired
	}
	var proof FeeProof
	if err := json.Unmarshal(data, &proof); err != nil {
		return nil, ErrInvalidFeeProof
	}
	proof.Mode = strings.ToUpper(strings.TrimSpace(proof.Mode))
	switch proof.Mode {
	case FeeModeOneshot, FeeModeLease, FeeModeAutopay, FeeModeFreeLocal:
	default:
		return nil, ErrInvalidFeeProof
	}
	return &proof, nil
}

type httpFeeVerifyRequest struct {
	RecordHash     string `json:"record_hash"`
	KeyHash        string `json:"key_hash"`
	Namespace      string `json:"namespace"`
	RecordSize     int    `json:"record_size"`
	ExpiryHeight   uint64 `json:"expiry_height"`
	FeeProofBase64 string `json:"fee_proof_base64"`
}

type httpFeeVerifyResponse struct {
	Code  int                    `json:"code,omitempty"`
	Msg   string                 `json:"msg,omitempty"`
	Valid *bool                  `json:"valid,omitempty"`
	Data  *httpFeeVerifyResponse `json:"data,omitempty"`
}

func (v HTTPFeeVerifier) VerifyFeeProof(recordHash, keyHash [32]byte, namespace string, recordSize int, expiryHeight uint64, feeProof []byte) error {
	endpoint := strings.TrimSpace(v.Endpoint)
	if endpoint == "" {
		return ErrInvalidFeeProof
	}
	reqBody := httpFeeVerifyRequest{
		RecordHash:     hex.EncodeToString(recordHash[:]),
		KeyHash:        hex.EncodeToString(keyHash[:]),
		Namespace:      namespace,
		RecordSize:     recordSize,
		ExpiryHeight:   expiryHeight,
		FeeProofBase64: base64.StdEncoding.EncodeToString(feeProof),
	}
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	client := v.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New(resp.Status)
	}
	var verifyResp httpFeeVerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&verifyResp); err != nil {
		return err
	}
	return verifyResp.err()
}

func (r httpFeeVerifyResponse) err() error {
	if r.Code != 0 {
		if r.Msg != "" {
			return errors.New(r.Msg)
		}
		return ErrInvalidFeeProof
	}
	src := &r
	if r.Data != nil {
		src = r.Data
	}
	if src.Valid != nil && *src.Valid {
		return nil
	}
	if src.Msg != "" {
		return errors.New(src.Msg)
	}
	return ErrInvalidFeeProof
}

func (v JSONFeeVerifier) VerifyFeeProof(recordHash, keyHash [32]byte, namespace string, recordSize int, expiryHeight uint64, feeProof []byte) error {
	if len(feeProof) == 0 {
		if v.AllowFreeLocal {
			return nil
		}
		return ErrFeeProofRequired
	}
	if recordSize < 0 {
		return ErrInvalidFeeProof
	}
	proof, err := ParseFeeProof(feeProof)
	if err != nil {
		return err
	}
	if proof.Mode == FeeModeFreeLocal && !v.AllowFreeLocal {
		return ErrInvalidFeeProof
	}
	if len(proof.ProofSignature) != 0 || v.RequireProofSignature {
		if err := VerifyFeeProofSignature(proof); err != nil {
			return err
		}
	}
	if proof.Namespace != namespace ||
		proof.RecordSize < uint32(recordSize) ||
		proof.ExpiryHeight != expiryHeight ||
		proof.KeyHash != chainhash.Hash(keyHash) {
		return ErrInvalidFeeProof
	}
	if proof.RecordHash != (chainhash.Hash{}) {
		if proof.RecordHash != chainhash.Hash(recordHash) {
			return ErrInvalidFeeProof
		}
	} else if !v.AllowMissingRecordHash {
		return ErrInvalidFeeProof
	}

	switch proof.Mode {
	case FeeModeOneshot:
		if proof.PoolContract == "" || proof.Payer == "" || proof.PaymentTxID == "" {
			return ErrInvalidFeeProof
		}
	case FeeModeLease:
		if proof.PoolContract == "" || proof.LeaseContract == "" || proof.PlanID == "" {
			return ErrInvalidFeeProof
		}
	case FeeModeAutopay:
		if proof.PoolContract == "" || proof.Payer == "" {
			return ErrInvalidFeeProof
		}
	case FeeModeFreeLocal:
		if !v.AllowFreeLocal {
			return ErrInvalidFeeProof
		}
	}
	return nil
}
