package dkvs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"

	indexercommon "github.com/sat20-labs/indexer/common"
)

var endpointIDKey = []byte("dkvs:endpoint-id")

func (i *Indexer) endpointID() string {
	if i == nil || i.db == nil {
		return ""
	}
	encoded, err := i.db.Read(endpointIDKey)
	if err == nil && len(encoded) != 0 {
		return string(encoded)
	}
	if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
		return ""
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(random[:])
	if err := i.db.Write(endpointIDKey, []byte(id)); err != nil {
		return ""
	}
	return id
}

func (i *Indexer) EndpointID() string {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	return i.endpointID()
}
