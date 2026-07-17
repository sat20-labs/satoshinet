package main

import (
	"testing"

	"github.com/sat20-labs/satoshinet/wire"
)

func TestDKVSSyncFilterMatchesKey(t *testing.T) {
	tests := []struct {
		filter wire.DKVSSyncFilter
		key    string
		want   bool
	}{
		{wire.DKVSSyncFilter{Type: "key", Target: "/name/alice"}, "/name/alice", true},
		{wire.DKVSSyncFilter{Type: "key", Target: "/name/alice"}, "/name/alice/x", false},
		{wire.DKVSSyncFilter{Type: "prefix", Target: "/svc/chat"}, "/svc/chat/config", true},
		{wire.DKVSSyncFilter{Type: "mailbox", Target: "/mail/box"}, "/mail/box/msg/1", true},
		{wire.DKVSSyncFilter{Type: "service", Target: "/svc/chat"}, "/svc/other/config", false},
	}
	for _, test := range tests {
		if got := dkvsSyncFilterMatchesKey(test.filter, test.key); got != test.want {
			t.Fatalf("filter=%#v key=%s got=%v want=%v", test.filter, test.key, got, test.want)
		}
	}
}
