package p2p

import (
	"testing"

	"github.com/sat20-labs/satoshinet/wire"
)

func TestDirectedNotifyMinerDoesNotTrustUnauthenticatedPeer(t *testing.T) {
	store := &handlerTestStore{}
	delivered := 0
	RegisterDirectedNotifyHandler(store, func(*wire.MsgDKVSNotify) error { delivered++; return nil })
	defer RegisterDirectedNotifyHandler(store, nil)
	handler := NewHandler(store, nil, nil, nil)
	handler.LocalServices = wire.SFNodeMiner
	message := &wire.MsgDKVSNotify{Target: "core", EventType: wire.DKVSNotifyEventMessage, Data: []byte("delivery")}
	handler.OnNotify(message)
	if delivered != 0 {
		t.Fatal("local miner bypassed directed source admission")
	}
	handler.TrustedSource = true
	handler.OnNotify(message)
	if delivered != 1 {
		t.Fatal("trusted relay failed to reach signed-delivery verification")
	}
}
