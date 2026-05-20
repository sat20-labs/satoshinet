package common

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

func EncodeDeployPayload(p DeployPayload) []byte {
	var buf bytes.Buffer
	buf.WriteByte(PayloadVersionV1)
	writeUvarint(&buf, p.GasLimit)
	writeUvarint(&buf, p.DeployNonce)
	buf.Write(p.InitCode)
	return buf.Bytes()
}

func DecodeDeployPayload(data []byte) (DeployPayload, error) {
	r, err := newPayloadReader(data)
	if err != nil {
		return DeployPayload{}, err
	}
	gasLimit, err := r.readUvarint()
	if err != nil {
		return DeployPayload{}, fmt.Errorf("decode deploy gas limit: %w", err)
	}
	nonce, err := r.readUvarint()
	if err != nil {
		return DeployPayload{}, fmt.Errorf("decode deploy nonce: %w", err)
	}
	return DeployPayload{
		GasLimit:    gasLimit,
		DeployNonce: nonce,
		InitCode:    r.readRemaining(),
	}, nil
}

func EncodeInvokePayload(p InvokePayload) []byte {
	var buf bytes.Buffer
	buf.WriteByte(PayloadVersionV1)
	writeUvarint(&buf, p.GasLimit)
	writeUvarint(&buf, p.CallNonce)
	buf.Write(p.Calldata)
	return buf.Bytes()
}

func DecodeInvokePayload(data []byte) (InvokePayload, error) {
	r, err := newPayloadReader(data)
	if err != nil {
		return InvokePayload{}, err
	}
	gasLimit, err := r.readUvarint()
	if err != nil {
		return InvokePayload{}, fmt.Errorf("decode invoke gas limit: %w", err)
	}
	nonce, err := r.readUvarint()
	if err != nil {
		return InvokePayload{}, fmt.Errorf("decode invoke nonce: %w", err)
	}
	return InvokePayload{
		GasLimit:  gasLimit,
		CallNonce: nonce,
		Calldata:  r.readRemaining(),
	}, nil
}

func EncodeResultPayload(p ResultPayload) []byte {
	var buf bytes.Buffer
	buf.WriteByte(PayloadVersionV1)
	buf.WriteByte(byte(p.Status))
	writeUvarint(&buf, uint64(p.ResultCount))
	if p.HasErrorInfo {
		buf.WriteByte(1)
		buf.Write(p.ErrorDigest[:])
	} else {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

func DecodeResultPayload(data []byte) (ResultPayload, error) {
	r, err := newPayloadReader(data)
	if err != nil {
		return ResultPayload{}, err
	}
	status, err := r.readByte()
	if err != nil {
		return ResultPayload{}, fmt.Errorf("decode result status: %w", err)
	}
	count, err := r.readUvarint()
	if err != nil {
		return ResultPayload{}, fmt.Errorf("decode result count: %w", err)
	}
	if count > uint64(^uint16(0)) {
		return ResultPayload{}, errors.New("result count overflows uint16")
	}
	flags, err := r.readByte()
	if err != nil {
		return ResultPayload{}, fmt.Errorf("decode result flags: %w", err)
	}
	p := ResultPayload{
		Status:       ResultStatus(status),
		ResultCount:  uint16(count),
		HasErrorInfo: flags&1 == 1,
	}
	if p.HasErrorInfo {
		digest, err := r.readFixed32()
		if err != nil {
			return ResultPayload{}, fmt.Errorf("decode result error digest: %w", err)
		}
		p.ErrorDigest = digest
	}
	if r.remaining() != 0 {
		return ResultPayload{}, errors.New("trailing result payload bytes")
	}
	return p, nil
}

func EncodeStateRootPayload(p StateRootPayload) []byte {
	out := make([]byte, 33)
	out[0] = PayloadVersionV1
	copy(out[1:], p.StateRoot[:])
	return out
}

func DecodeStateRootPayload(data []byte) (StateRootPayload, error) {
	r, err := newPayloadReader(data)
	if err != nil {
		return StateRootPayload{}, err
	}
	root, err := r.readFixed32()
	if err != nil {
		return StateRootPayload{}, fmt.Errorf("decode state root: %w", err)
	}
	if r.remaining() != 0 {
		return StateRootPayload{}, errors.New("trailing state root payload bytes")
	}
	return StateRootPayload{StateRoot: root}, nil
}

type payloadReader struct {
	*bytes.Reader
}

func newPayloadReader(data []byte) (*payloadReader, error) {
	r := &payloadReader{Reader: bytes.NewReader(data)}
	version, err := r.readByte()
	if err != nil {
		return nil, fmt.Errorf("decode payload version: %w", err)
	}
	if version != PayloadVersionV1 {
		return nil, fmt.Errorf("unsupported payload version %d", version)
	}
	return r, nil
}

func (r *payloadReader) readByte() (byte, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, io.ErrUnexpectedEOF
	}
	return b, nil
}

func (r *payloadReader) readUvarint() (uint64, error) {
	v, err := binary.ReadUvarint(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return 0, io.ErrUnexpectedEOF
		}
		return 0, err
	}
	return v, nil
}

func (r *payloadReader) readFixed32() ([32]byte, error) {
	var out [32]byte
	if _, err := io.ReadFull(r, out[:]); err != nil {
		return out, err
	}
	return out, nil
}

func (r *payloadReader) readRemaining() []byte {
	out := make([]byte, r.Len())
	_, _ = io.ReadFull(r, out)
	return out
}

func (r *payloadReader) remaining() int {
	return r.Len()
}

func writeUvarint(w *bytes.Buffer, v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	w.Write(tmp[:n])
}
