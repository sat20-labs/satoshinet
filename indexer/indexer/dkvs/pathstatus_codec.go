package dkvs

import (
	"bytes"
	"math"

	"github.com/sat20-labs/satoshinet/wire"
)

const pathStatusMagic = "DKPS\x01"

// Local status is not part of the path state root. Its compact encoding keeps
// the path binding and all retry fields without adding migration state.
func marshalPathStatus(status *PathLocalStatus) ([]byte, error) {
	if status == nil || status.Path == "" {
		return nil, ErrInvalidRecord
	}
	var buf bytes.Buffer
	buf.WriteString(pathStatusMagic)
	for _, field := range []string{status.Path, status.LastSyncPeer, status.LocalRetryState} {
		if err := wire.WriteVarString(&buf, 0, field); err != nil {
			return nil, err
		}
	}
	for _, value := range []uint64{status.UpdatedAt, status.LastSyncAt} {
		if err := wire.WriteVarInt(&buf, 0, value); err != nil {
			return nil, err
		}
	}
	var flags byte
	if status.Dirty {
		flags |= 1
	}
	if status.Stale {
		flags |= 2
	}
	buf.WriteByte(flags)
	return buf.Bytes(), nil
}

func unmarshalPathStatus(path string, encoded []byte) (*PathLocalStatus, error) {
	if path == "" || len(encoded) < len(pathStatusMagic) || uint64(len(encoded)) > math.MaxUint32 ||
		string(encoded[:len(pathStatusMagic)]) != pathStatusMagic {
		return nil, ErrInvalidRecord
	}
	r := bytes.NewReader(encoded[len(pathStatusMagic):])
	var status PathLocalStatus
	for _, field := range []*string{&status.Path, &status.LastSyncPeer, &status.LocalRetryState} {
		value, err := wire.ReadVarBytes(r, 0, uint32(r.Len()), "path local status")
		if err != nil {
			return nil, ErrInvalidRecord
		}
		*field = string(value)
	}
	for _, field := range []*uint64{&status.UpdatedAt, &status.LastSyncAt} {
		value, err := wire.ReadVarInt(r, 0)
		if err != nil {
			return nil, ErrInvalidRecord
		}
		*field = value
	}
	flags, err := r.ReadByte()
	if err != nil || flags & ^byte(3) != 0 || r.Len() != 0 || status.Path != path {
		return nil, ErrInvalidRecord
	}
	status.Dirty = flags&1 != 0
	status.Stale = flags&2 != 0
	return &status, nil
}
