package indexer

import (
	"errors"
	"strings"
	"testing"
)

func TestRunValidationAssetReplayConvertsPanicToError(t *testing.T) {
	err := runValidationAssetReplay(func() error {
		panic("candidate asset state conflict")
	})
	if err == nil {
		t.Fatal("expected candidate replay panic to become an error")
	}
	if !strings.Contains(err.Error(), "asset validation replay panic") ||
		!strings.Contains(err.Error(), "candidate asset state conflict") {
		t.Fatalf("unexpected recovered error: %v", err)
	}
}

func TestRunValidationAssetReplayPreservesReturnedError(t *testing.T) {
	want := errors.New("candidate validation failed")
	if got := runValidationAssetReplay(func() error { return want }); !errors.Is(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
