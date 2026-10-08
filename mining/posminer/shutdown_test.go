package posminer

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPOSMinerShutdownWaitsForScheduledWork(t *testing.T) {
	m := lifecycleMiner(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once, enteredOnce sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	m.cfg.IsCurrent = func() bool { enteredOnce.Do(func() { close(entered) }); <-release; return false }
	// Speed up the existing scheduled worker; no separate mining harness.
	previousInterval := CheckingInterval
	CheckingInterval = 1
	t.Cleanup(func() { CheckingInterval = previousInterval })
	require.NoError(t, m.Start())
	t.Cleanup(func() { unblock(); m.Stop(); m.WaitForShutdown() })
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled worker did not start")
	}
	m.Stop()
	stopped := make(chan struct{})
	go func() { m.WaitForShutdown(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("miner shutdown returned while a scheduled operation was active")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled worker did not stop")
	}
}
