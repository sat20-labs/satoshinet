package dkvs

import (
	"errors"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
)

var serviceTransferKeyPrefix = []byte("dkvs:service-transfer:")

func serviceTransferDBKey(service string) []byte {
	out := make([]byte, 0, len(serviceTransferKeyPrefix)+len(service))
	out = append(out, serviceTransferKeyPrefix...)
	out = append(out, service...)
	return out
}

func (i *Indexer) serviceTransferDirty(service string) (bool, error) {
	_, err := i.db.Read(serviceTransferDBKey(service))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return false, nil
	}
	return false, err
}

func (i *Indexer) authorityDirty(parsed ParsedKey) (bool, error) {
	if len(parsed.Segments) == 0 {
		return false, nil
	}
	switch parsed.Namespace {
	case "name":
		return i.nameTransferDirty(parsed.Segments[0])
	case "svc":
		return i.serviceTransferDirty(parsed.Segments[0])
	default:
		return false, nil
	}
}

func (i *Indexer) clearAuthorityDirtyBatchLocked(batch indexercommon.WriteBatch, parsed ParsedKey) error {
	if len(parsed.Segments) == 0 {
		return nil
	}
	switch parsed.Namespace {
	case "name":
		return batch.Delete(nameTransferDBKey(parsed.Segments[0]))
	case "svc":
		return batch.Delete(serviceTransferDBKey(parsed.Segments[0]))
	default:
		return nil
	}
}

func (i *Indexer) notifyNameTransfersLocked(names []string) error {
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	height := i.currentHeight()
	now := currentUnixMilli()
	changed := false
	for _, name := range names {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" {
			continue
		}
		name = NormalizeNameID(name)
		if len(name) > MaxKeySegmentSize || !validSegment(name) {
			return ErrInvalidKey
		}
		if err := batch.Put(nameTransferDBKey(name), []byte{1}); err != nil {
			return err
		}
		if err := batch.Put(serviceTransferDBKey(name), []byte{1}); err != nil {
			return err
		}
		if err := i.zeroServicePathMetaBatchLocked(batch, name, height, now); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return batch.Flush()
}
