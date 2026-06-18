package framework

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	contract "github.com/sat20-labs/satoshinet/contract"
)

type PayloadReader struct {
	*bytes.Reader
}

func NewPayloadReader(data []byte) (*PayloadReader, error) {
	r := &PayloadReader{Reader: bytes.NewReader(data)}
	version, err := r.ReadByteStrict()
	if err != nil {
		return nil, fmt.Errorf("decode payload version: %w", err)
	}
	if version != contract.PayloadVersionV1 {
		return nil, fmt.Errorf("unsupported payload version %d", version)
	}
	return r, nil
}

func (r *PayloadReader) ReadByteStrict() (byte, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, io.ErrUnexpectedEOF
	}
	return b, nil
}

func (r *PayloadReader) ReadUvarint() (uint64, error) {
	v, err := binary.ReadUvarint(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return 0, io.ErrUnexpectedEOF
		}
		return 0, err
	}
	return v, nil
}

func (r *PayloadReader) ReadBytes() ([]byte, error) {
	n, err := r.ReadUvarint()
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

func (r *PayloadReader) ReadString() (string, error) {
	b, err := r.ReadBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *PayloadReader) Remaining() int {
	return r.Len()
}

func WritePayloadVersion(buf *bytes.Buffer) {
	buf.WriteByte(contract.PayloadVersionV1)
}

func WriteUvarint(buf *bytes.Buffer, v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	buf.Write(tmp[:n])
}

func WriteBytes(buf *bytes.Buffer, data []byte) {
	WriteUvarint(buf, uint64(len(data)))
	buf.Write(data)
}

func WriteString(buf *bytes.Buffer, s string) {
	WriteBytes(buf, []byte(s))
}

type NamedDeployPayload struct {
	GasLimit        int64
	Name            string
	Version         uint32
	Deployer        string
	Random          []byte
	ContractContent []byte
}

type ActionInvokePayload struct {
	GasLimit  int64
	CallNonce uint64
	Action    string
	Param     []byte
}
