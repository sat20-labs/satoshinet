package blockchain

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectedNotificationCannotRewindDurableUTXOState(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "legacy"
		if active {
			name = "POS"
		}
		t.Run(name, func(t *testing.T) {
			chain, ready, key, pub, first, teardown := setupPOSChain(t)
			defer teardown()
			if !active {
				chain.chainParams.POSV2Height = 100
			}
			accept := func(block *wire.MsgBlock) error {
				if active {
					_, err := chain.ApprovePOSBlock(block, pub, signPOS(key))
					return err
				}
				_, _, err := chain.ProcessBlock(btcutil.NewBlock(block), BFFastAdd)
				return err
			}
			input := wire.OutPoint{Hash: chainhash.Hash{42}}
			require.NoError(t, chain.db.Update(func(tx database.Tx) error {
				return dbPutUtxoEntry(tx.Metadata().Bucket(utxoSetBucketName), input, &UtxoEntry{amount: 100, pkScript: []byte{txscript.OP_TRUE}, blockHeight: 0})
			}))
			spend := wire.NewMsgTx(2)
			spend.AddTxIn(wire.NewTxIn(&input, nil, nil))
			spend.AddTxOut(wire.NewTxOut(100, nil, []byte{txscript.OP_TRUE}))
			first.Transactions = append(first.Transactions, spend)
			updatePOSCommitments(first)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			require.Zero(t, chain.utxoCache.maxTotalMemoryUsage)
			var connectedHeight atomic.Int32
			chain.onBlockConnected = connectedHeight.Store
			chain.Subscribe(func(n *Notification) {
				if n.Type != NTBlockConnected {
					return
				}
				block := n.Data.(*btcutil.Block)
				// Notification callbacks must still be able to read the committed chain.
				require.True(t, chain.CanServeBlock(block.Hash()))
				if block.Height() == 1 {
					close(entered)
					<-release
				}

			})
			aDone := make(chan error, 1)
			go func() { err := accept(first); aDone <- err }()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("first notification did not begin")
			}
			ready.setTip(1, first.BlockHash())
			require.NoError(t, ready.seq.MoveMiningAddr(1, ready.seq.GetCurrentMiningAddr()))
			second := first.Copy()
			second.Header.PrevBlock = first.BlockHash()
			second.Header.Timestamp = first.Header.Timestamp.Add(time.Second)
			sig := ecdsa.Sign(key, chainhash.HashB(scommon.GetScriptSignData(2, 0))).Serialize()
			var err error
			second.Transactions[0].TxIn[0].SignatureScript, err = txscript.NewScriptBuilder().AddInt64(2).AddInt64(0).AddData(sig).Script()
			require.NoError(t, err)
			spend2 := wire.NewMsgTx(2)
			spend2.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: spend.TxHash()}, nil, nil))
			spend2.AddTxOut(wire.NewTxOut(100, nil, []byte{txscript.OP_TRUE}))
			second.Transactions[1] = spend2
			updatePOSCommitments(second)
			bDone := make(chan error, 1)
			go func() { err := accept(second); bDone <- err }()
			// Complete B while A's notification is still paused. This fix
			// preserves concurrent notification behavior; it must not serialize B.
			select {
			case bErr := <-bDone:
				once.Do(func() { close(release) })
				require.NoError(t, <-aDone)
				require.NoError(t, bErr)
			case <-time.After(10 * time.Second):
				once.Do(func() { close(release) })
				<-aDone
				<-bDone
				t.Fatal("B did not complete while A's notification was paused")
			}
			assert.Equal(t, int32(2), connectedHeight.Load(), "completed block hook must not run again at A's old height")
			require.Equal(t, second.BlockHash(), chain.BestSnapshot().Hash)
			require.NoError(t, chain.db.View(func(tx database.Tx) error {
				hash := second.BlockHash()
				assert.Equal(t, hash[:], dbFetchUtxoStateConsistency(tx))
				return nil
			}))
			// Close the DB directly, without graceful current-tip cache flushing.
			require.NoError(t, chain.db.Close())
			reopened, err := database.Open(testDbType, filepath.Join(testDbRoot, t.Name()), blockDataNet)
			require.NoError(t, err)
			defer reopened.Close()
			params := *chain.chainParams
			genesis := params.GenesisBlock.BlockHash()
			params.GenesisHash = &genesis
			restored, err := New(&Config{DB: reopened, ChainParams: &params, TimeSource: NewMedianTime()})
			require.NoError(t, err, "the already-spent input must not be replayed a second time")
			require.Equal(t, second.BlockHash(), restored.BestSnapshot().Hash)
			require.NoError(t, reopened.View(func(tx database.Tx) error {
				bucket := tx.Metadata().Bucket(utxoSetBucketName)
				for _, spent := range []wire.OutPoint{input, {Hash: spend.TxHash()}} {
					entry, err := dbFetchUtxoEntry(tx, bucket, spent)
					require.NoError(t, err)
					require.Nil(t, entry)
				}
				entry, err := dbFetchUtxoEntry(tx, bucket, wire.OutPoint{Hash: spend2.TxHash()})
				require.NoError(t, err)
				require.NotNil(t, entry)
				require.Equal(t, int64(100), entry.Amount())
				return nil
			}))

		})
	}
}
