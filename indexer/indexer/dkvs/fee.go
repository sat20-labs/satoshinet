package dkvs

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

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
	AllowFreeLocal bool
}

type HTTPFeeVerifier struct {
	Endpoint string
	Client   *http.Client
}

func NewOneshotFeeProof(key, namespace string, recordSize uint32, expiryHeight uint64, poolContract, payer, paymentTxID, paidAmount string) (*FeeProof, error) {
	if _, err := parseFeeProofKeyNamespace(key, namespace); err != nil {
		return nil, err
	}
	_ = recordSize
	_ = expiryHeight
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
		PaidAmount:   paidAmount,
	}, nil
}

func NewLeaseFeeProof(key, namespace string, recordSize uint32, expiryHeight uint64, poolContract, leaseContract, planID string) (*FeeProof, error) {
	if _, err := parseFeeProofKeyNamespace(key, namespace); err != nil {
		return nil, err
	}
	_ = recordSize
	_ = expiryHeight
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
	}, nil
}

func NewAutopayFeeProof(key, namespace string, recordSize uint32, expiryHeight uint64, poolContract, payer string) (*FeeProof, error) {
	if _, err := parseFeeProofKeyNamespace(key, namespace); err != nil {
		return nil, err
	}
	_ = recordSize
	_ = expiryHeight
	_ = payer
	poolContract = strings.TrimSpace(poolContract)
	if poolContract == "" {
		return nil, ErrInvalidFeeProof
	}
	return &FeeProof{
		Mode:         FeeModeAutopay,
		PoolContract: poolContract,
	}, nil
}

func NewFreeLocalFeeProof(key, namespace string, recordSize uint32, expiryHeight uint64) (*FeeProof, error) {
	if _, err := parseFeeProofKeyNamespace(key, namespace); err != nil {
		return nil, err
	}
	_ = recordSize
	_ = expiryHeight
	return &FeeProof{
		Mode: FeeModeFreeLocal,
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
	var buf bytes.Buffer
	buf.WriteByte(1)
	mode, err := feeProofModeCode(proofCopy.Mode)
	if err != nil {
		return nil, err
	}
	buf.WriteByte(mode)
	switch proofCopy.Mode {
	case FeeModeOneshot:
		if writeFeeProofString(&buf, strings.TrimSpace(proofCopy.PoolContract)) != nil ||
			writeFeeProofString(&buf, strings.TrimSpace(proofCopy.Payer)) != nil ||
			writeFeeProofString(&buf, strings.TrimSpace(proofCopy.PaymentTxID)) != nil ||
			writeFeeProofString(&buf, strings.TrimSpace(proofCopy.PaidAmount)) != nil {
			return nil, ErrInvalidFeeProof
		}
	case FeeModeLease:
		if writeFeeProofString(&buf, strings.TrimSpace(proofCopy.PoolContract)) != nil ||
			writeFeeProofString(&buf, strings.TrimSpace(proofCopy.LeaseContract)) != nil ||
			writeFeeProofString(&buf, strings.TrimSpace(proofCopy.PlanID)) != nil {
			return nil, ErrInvalidFeeProof
		}
	case FeeModeAutopay:
		if writeFeeProofString(&buf, strings.TrimSpace(proofCopy.PoolContract)) != nil {
			return nil, ErrInvalidFeeProof
		}
	case FeeModeFreeLocal:
	default:
		return nil, ErrInvalidFeeProof
	}
	encoded := buf.Bytes()
	if len(encoded) > wire.MaxDKVSFeeProofSize {
		return nil, ErrInvalidFeeProof
	}
	if _, err := ParseFeeProof(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

func FeeAnchorHash(record *wire.DKVSRecord) chainhash.Hash {
	if record == nil {
		return chainhash.Hash{}
	}
	recordCopy := *record
	recordCopy.Signature = nil
	return chainhash.DoubleHashH(canonicalRecordBytes(&recordCopy, false))
}

func ParseFeeProof(data []byte) (*FeeProof, error) {
	if len(data) == 0 {
		return nil, ErrFeeProofRequired
	}
	if len(data) > wire.MaxDKVSFeeProofSize {
		return nil, ErrInvalidFeeProof
	}
	r := bytes.NewReader(data)
	version, err := r.ReadByte()
	if err != nil || version != 1 {
		return nil, ErrInvalidFeeProof
	}
	modeCode, err := r.ReadByte()
	if err != nil {
		return nil, ErrInvalidFeeProof
	}
	mode, err := feeProofModeName(modeCode)
	if err != nil {
		return nil, err
	}
	proof := &FeeProof{Mode: mode}
	switch proof.Mode {
	case FeeModeOneshot:
		proof.PoolContract, err = readFeeProofString(r)
		if err != nil {
			return nil, err
		}
		proof.Payer, err = readFeeProofString(r)
		if err != nil {
			return nil, err
		}
		proof.PaymentTxID, err = readFeeProofString(r)
		if err != nil {
			return nil, err
		}
		proof.PaidAmount, err = readFeeProofString(r)
		if err != nil {
			return nil, err
		}
	case FeeModeLease:
		proof.PoolContract, err = readFeeProofString(r)
		if err != nil {
			return nil, err
		}
		proof.LeaseContract, err = readFeeProofString(r)
		if err != nil {
			return nil, err
		}
		proof.PlanID, err = readFeeProofString(r)
		if err != nil {
			return nil, err
		}
	case FeeModeAutopay:
		proof.PoolContract, err = readFeeProofString(r)
		if err != nil {
			return nil, err
		}
	case FeeModeFreeLocal:
	default:
		return nil, ErrInvalidFeeProof
	}
	if r.Len() != 0 {
		return nil, ErrInvalidFeeProof
	}
	return proof, nil
}

func feeProofModeCode(mode string) (byte, error) {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case FeeModeOneshot:
		return 1, nil
	case FeeModeLease:
		return 2, nil
	case FeeModeAutopay:
		return 3, nil
	case FeeModeFreeLocal:
		return 4, nil
	default:
		return 0, ErrInvalidFeeProof
	}
}

func feeProofModeName(code byte) (string, error) {
	switch code {
	case 1:
		return FeeModeOneshot, nil
	case 2:
		return FeeModeLease, nil
	case 3:
		return FeeModeAutopay, nil
	case 4:
		return FeeModeFreeLocal, nil
	default:
		return "", ErrInvalidFeeProof
	}
}

func writeFeeProofString(buf *bytes.Buffer, value string) error {
	if len(value) > wire.MaxDKVSFeeProofSize {
		return ErrInvalidFeeProof
	}
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(value)))
	buf.Write(lenBuf[:n])
	buf.WriteString(value)
	return nil
}

func readFeeProofString(r *bytes.Reader) (string, error) {
	length, err := binary.ReadUvarint(r)
	if err != nil {
		return "", ErrInvalidFeeProof
	}
	if length > uint64(wire.MaxDKVSFeeProofSize) || length > uint64(r.Len()) {
		return "", ErrInvalidFeeProof
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", ErrInvalidFeeProof
	}
	return string(buf), nil
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
	_ = recordHash
	_ = keyHash
	_ = namespace
	_ = expiryHeight
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
		if proof.PoolContract == "" {
			return ErrInvalidFeeProof
		}
	case FeeModeFreeLocal:
		if !v.AllowFreeLocal {
			return ErrInvalidFeeProof
		}
	}
	return nil
}
