package dkvs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var implementationVersionFilePattern = regexp.MustCompile(`_v[0-9]+\.go$`)

func TestDKVSFinalArchitectureGuards(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		if strings.HasPrefix(text, "//go:build ignore") {
			t.Errorf("DKVS package retains a build-ignore placeholder: %s", name)
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if implementationVersionFilePattern.MatchString(name) {
			t.Errorf("DKVS implementation file has version suffix: %s", name)
		}
		for _, forbidden := range []string{
			"PathWritePrecondition",
			"EndpointPathState",
			"EndpointEpoch",
			"SubscriptionChangeLog",
			"WalletSubscriptionCursor",
			"time.NewTicker(250 * time.Millisecond)",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s still contains removed wallet/application mechanism %q", name, forbidden)
			}
		}
	}
}
