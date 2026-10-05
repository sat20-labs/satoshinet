package main

import "testing"

func TestSatoshiNetReleaseVersion(t *testing.T) {
	if got := version(); got != "1.0.0" {
		t.Fatalf("version() = %q, want 1.0.0", got)
	}
}
