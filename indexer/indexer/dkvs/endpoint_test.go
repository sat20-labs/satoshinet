package dkvs

import (
	"errors"
	"testing"
)

func TestEndpointIDUsesImmutableCoreNodeIdentity(t *testing.T) {
	idx := testIndexer(t)
	if got := idx.EndpointID(); got != "test-core-node" {
		t.Fatalf("endpoint=%q", got)
	}
	if err := idx.SetEndpointID("test-core-node"); err != nil {
		t.Fatalf("same CoreNode identity rejected: %v", err)
	}
	if err := idx.SetEndpointID("different-core-node"); !errors.Is(err, ErrEndpointMismatch) {
		t.Fatalf("CoreNode identity switch err=%v", err)
	}
}
