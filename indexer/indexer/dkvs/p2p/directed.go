package p2p

import (
	"sync"

	"github.com/sat20-labs/satoshinet/wire"
)

type DirectedNotifyHandler func(*wire.MsgDKVSNotify) error

var directedNotifyHandlers sync.Map

// RegisterDirectedNotifyHandler attaches the application-level targeted Notify
// handler to one DKVS Store instance. Targeted messages never enter ordinary
// DKVS record replication.
func RegisterDirectedNotifyHandler(store Store, handler DirectedNotifyHandler) {
	if store == nil {
		return
	}
	if handler == nil {
		directedNotifyHandlers.Delete(store)
		return
	}
	directedNotifyHandlers.Store(store, handler)
}

func handleDirectedNotify(store Store, message *wire.MsgDKVSNotify) (bool, error) {
	if message == nil || message.Target == "" {
		return false, nil
	}
	value, ok := directedNotifyHandlers.Load(store)
	if !ok {
		return true, nil
	}
	return true, value.(DirectedNotifyHandler)(message)
}
