package dkvs

import (
	"strings"
)

func (i *Indexer) endpointID() string {
	if i == nil {
		return ""
	}
	return i.endpointIdentity
}

func (i *Indexer) EndpointID() string {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.endpointID()
}

// SetEndpointID binds endpoint-local DKVS state to the CoreNode identity. A
// running Indexer may be initialized before the node wallet is available, but
// its identity is immutable once assigned.
func (i *Indexer) SetEndpointID(endpointID string) error {
	if i == nil || strings.TrimSpace(endpointID) == "" {
		return ErrStaleEndpoint
	}
	endpointID = strings.TrimSpace(endpointID)
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.endpointIdentity != "" && i.endpointIdentity != endpointID {
		return ErrEndpointMismatch
	}
	i.endpointIdentity = endpointID
	return nil
}
