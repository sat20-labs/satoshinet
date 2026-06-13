package agent

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

type DeployPayload struct {
	GasLimit        uint64
	Subtype         string
	AgentVersion    uint32
	Deployer        string
	Random          []byte
	ContractContent []byte
}

type InvokePayload struct {
	GasLimit  uint64
	CallNonce uint64
	Action    string
	Param     []byte
}

func EncodeDeployPayload(p DeployPayload) ([]byte, error) {
	if p.Subtype == "" {
		return nil, errors.New("agent subtype is empty")
	}
	if p.AgentVersion == 0 {
		return nil, errors.New("agent version is zero")
	}
	if p.Deployer == "" {
		return nil, errors.New("deployer is empty")
	}
	if len(p.Random) == 0 {
		return nil, errors.New("random value is empty")
	}
	if len(p.ContractContent) == 0 {
		return nil, errors.New("contract content is empty")
	}

	var buf bytes.Buffer
	buf.WriteByte(contractcommon.PayloadVersionV1)
	writeUvarint(&buf, p.GasLimit)
	writeString(&buf, p.Subtype)
	writeUvarint(&buf, uint64(p.AgentVersion))
	writeString(&buf, p.Deployer)
	writeBytes(&buf, p.Random)
	writeBytes(&buf, p.ContractContent)
	return buf.Bytes(), nil
}

func DecodeDeployPayload(data []byte) (DeployPayload, error) {
	r, err := newReader(data)
	if err != nil {
		return DeployPayload{}, err
	}
	gasLimit, err := r.readUvarint()
	if err != nil {
		return DeployPayload{}, fmt.Errorf("decode gas limit: %w", err)
	}
	subtype, err := r.readString()
	if err != nil {
		return DeployPayload{}, fmt.Errorf("decode subtype: %w", err)
	}
	version, err := r.readUvarint()
	if err != nil {
		return DeployPayload{}, fmt.Errorf("decode agent version: %w", err)
	}
	if version == 0 || version > uint64(^uint32(0)) {
		return DeployPayload{}, errors.New("invalid agent version")
	}
	deployer, err := r.readString()
	if err != nil {
		return DeployPayload{}, fmt.Errorf("decode deployer: %w", err)
	}
	random, err := r.readBytes()
	if err != nil {
		return DeployPayload{}, fmt.Errorf("decode random: %w", err)
	}
	content, err := r.readBytes()
	if err != nil {
		return DeployPayload{}, fmt.Errorf("decode contract content: %w", err)
	}
	if r.remaining() != 0 {
		return DeployPayload{}, errors.New("trailing deploy payload bytes")
	}
	return DeployPayload{
		GasLimit:        gasLimit,
		Subtype:         subtype,
		AgentVersion:    uint32(version),
		Deployer:        deployer,
		Random:          random,
		ContractContent: content,
	}, nil
}

func EncodeInvokePayload(p InvokePayload) ([]byte, error) {
	if p.Action == "" {
		return nil, errors.New("invoke action is empty")
	}
	var buf bytes.Buffer
	buf.WriteByte(contractcommon.PayloadVersionV1)
	writeUvarint(&buf, p.GasLimit)
	writeUvarint(&buf, p.CallNonce)
	writeString(&buf, p.Action)
	writeBytes(&buf, p.Param)
	return buf.Bytes(), nil
}

func DecodeInvokePayload(data []byte) (InvokePayload, error) {
	r, err := newReader(data)
	if err != nil {
		return InvokePayload{}, err
	}
	gasLimit, err := r.readUvarint()
	if err != nil {
		return InvokePayload{}, fmt.Errorf("decode gas limit: %w", err)
	}
	callNonce, err := r.readUvarint()
	if err != nil {
		return InvokePayload{}, fmt.Errorf("decode call nonce: %w", err)
	}
	action, err := r.readString()
	if err != nil {
		return InvokePayload{}, fmt.Errorf("decode action: %w", err)
	}
	param, err := r.readBytes()
	if err != nil {
		return InvokePayload{}, fmt.Errorf("decode param: %w", err)
	}
	if r.remaining() != 0 {
		return InvokePayload{}, errors.New("trailing invoke payload bytes")
	}
	return InvokePayload{
		GasLimit:  gasLimit,
		CallNonce: callNonce,
		Action:    action,
		Param:     param,
	}, nil
}

type payloadReader struct {
	*bytes.Reader
}

func newReader(data []byte) (*payloadReader, error) {
	r := &payloadReader{Reader: bytes.NewReader(data)}
	version, err := r.readByte()
	if err != nil {
		return nil, fmt.Errorf("decode payload version: %w", err)
	}
	if version != contractcommon.PayloadVersionV1 {
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

func (r *payloadReader) readBytes() ([]byte, error) {
	n, err := r.readUvarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Len()) {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, int(n))
	_, err = io.ReadFull(r, out)
	return out, err
}

func (r *payloadReader) readString() (string, error) {
	b, err := r.readBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *payloadReader) remaining() int {
	return r.Len()
}

func writeString(buf *bytes.Buffer, s string) {
	writeBytes(buf, []byte(s))
}

func writeBytes(buf *bytes.Buffer, data []byte) {
	writeUvarint(buf, uint64(len(data)))
	buf.Write(data)
}

func writeUvarint(buf *bytes.Buffer, v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	buf.Write(tmp[:n])
}
