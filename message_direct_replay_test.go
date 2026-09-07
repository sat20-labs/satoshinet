package main

import (
	"testing"
	"time"
)

func TestDirectReplayWindowReservationExpiryAndBound(t *testing.T) {
	now := time.Unix(100, 0)
	window := newDirectReplayWindow(2, time.Minute)
	if duplicate, pending := window.begin("a", now); duplicate || pending {
		t.Fatalf("first reservation duplicate=%v pending=%v", duplicate, pending)
	}
	if duplicate, pending := window.begin("a", now); duplicate || !pending {
		t.Fatalf("concurrent reservation duplicate=%v pending=%v", duplicate, pending)
	}
	window.finish("a", false, now)
	if duplicate, pending := window.begin("a", now); duplicate || pending {
		t.Fatalf("failed delivery stayed reserved duplicate=%v pending=%v", duplicate, pending)
	}
	window.finish("a", true, now)
	if duplicate, pending := window.begin("a", now); !duplicate || pending {
		t.Fatalf("committed delivery not detected duplicate=%v pending=%v", duplicate, pending)
	}

	for _, key := range []string{"b", "c"} {
		if duplicate, pending := window.begin(key, now); duplicate || pending {
			t.Fatalf("reserve %s duplicate=%v pending=%v", key, duplicate, pending)
		}
		window.finish(key, true, now)
	}
	if len(window.entries) != 2 {
		t.Fatalf("bounded entries=%d want=2", len(window.entries))
	}
	if duplicate, pending := window.begin("a", now); duplicate || pending {
		t.Fatalf("oldest entry was not evicted duplicate=%v pending=%v", duplicate, pending)
	}
	window.finish("a", false, now)
	if duplicate, pending := window.begin("b", now.Add(time.Minute)); duplicate || pending {
		t.Fatalf("expired entry duplicate=%v pending=%v", duplicate, pending)
	}
}
