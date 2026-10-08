package netsync

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RPC and scheduled mining can still be waiting on these requests when the
// block handler observes quit. Both the queue send and reply wait must stop.
func TestShutdownReleasesSyncQueries(t *testing.T) {
	for _, request := range []string{"peer", "current", "block"} {
		for _, pendingReply := range []bool{false, true} {
			t.Run(request+map[bool]string{false: "/send", true: "/reply"}[pendingReply], func(t *testing.T) {
				sm := &SyncManager{msgChan: make(chan interface{}), quit: make(chan struct{})}
				done := make(chan struct{})
				go func() {
					defer close(done)
					switch request {
					case "peer":
						assert.Zero(t, sm.SyncPeerID())
					case "current":
						assert.False(t, sm.IsCurrent())
					case "block":
						orphan, err := sm.ProcessBlock(nil, blockchain.BFNone)
						assert.False(t, orphan)
						assert.Error(t, err)
					}
				}()
				var message interface{}
				if pendingReply {
					select {
					case message = <-sm.msgChan:
					case <-time.After(time.Second):
						t.Fatal("request did not enter queue")
					}
				}
				close(sm.quit)
				select {
				case <-done:
				case <-time.After(500 * time.Millisecond):
					t.Fatal("request waited for exited block handler")
				}
				if pendingReply {
					replied := make(chan struct{})
					go func() {
						defer close(replied)
						switch msg := message.(type) {
						case getSyncPeerMsg:
							msg.reply <- 0
						case isCurrentMsg:
							msg.reply <- false
						case processBlockMsg:
							msg.reply <- processBlockResponse{}
						}
					}()
					select {
					case <-replied:
					case <-time.After(time.Second):
						t.Fatal("block handler blocked on abandoned reply")
					}
				}
			})
		}
	}
}

// Gate the real blockExists DB read so shutdown happens while the actual
// worker validates a rejected block, after its ProcessBlock caller has left.
type shutdownBlockDB struct {
	database.DB
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (d *shutdownBlockDB) View(f func(database.Tx) error) error {
	if d.armed.CompareAndSwap(true, false) {
		close(d.entered)
		<-d.release
	}
	return d.DB.View(f)
}

func TestShutdownRejectedBlockRepliesOnce(t *testing.T) {
	DisableLog()
	params := chaincfg.RegressionNetParams
	genesis := params.GenesisBlock.BlockHash()
	params.GenesisHash = &genesis
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: &genesis}}
	db, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	defer db.Close()
	gated := &shutdownBlockDB{DB: db, entered: make(chan struct{}), release: make(chan struct{})}
	chain, err := blockchain.New(&blockchain.Config{DB: gated, ChainParams: &params, TimeSource: blockchain.NewMedianTime()})
	require.NoError(t, err)
	sm, err := New(&Config{Chain: chain, ChainParams: &params, MaxPeers: 1, DisableCheckpoints: true, PeerNotifier: recoveryNotifier{}})
	require.NoError(t, err)
	// This malformed block must be rejected by the real validator.
	header := params.GenesisBlock.Header
	header.Nonce++
	block := btcutil.NewBlock(&wire.MsgBlock{Header: header})
	_, _, err = chain.ProcessBlock(block, blockchain.BFNoPoWCheck)
	var ruleErr blockchain.RuleError
	require.ErrorAs(t, err, &ruleErr)
	require.Equal(t, blockchain.ErrNoTransactions, ruleErr.ErrorCode)
	sm.Start()
	gated.armed.Store(true)
	caller := make(chan error, 1)
	go func() { _, err := sm.ProcessBlock(block, blockchain.BFNoPoWCheck); caller <- err }()
	var once sync.Once
	unblock := func() { once.Do(func() { close(gated.release) }) }
	defer unblock()
	select {
	case <-gated.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not reach the real DB read")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- sm.Stop() }()
	<-sm.quit
	select {
	case err := <-caller:
		require.ErrorContains(t, err, "shutting down")
	case <-time.After(time.Second):
		t.Fatal("caller did not leave on quit")
	}
	unblock()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("rejected-block worker blocked while replying to the exited caller")
	}
	require.Zero(t, chain.BestSnapshot().Height)
}
