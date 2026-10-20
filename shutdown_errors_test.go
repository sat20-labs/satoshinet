package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	assetindexer "github.com/sat20-labs/satoshinet/indexer/indexer"
	"github.com/sat20-labs/satoshinet/mempool"
	"github.com/sat20-labs/satoshinet/mining/posminer"
	"github.com/stretchr/testify/require"
)

type feeSaveFailureDB struct {
	database.DB
	stage string
	err   error
}

func (d *feeSaveFailureDB) Update(fn func(database.Tx) error) error {
	if d.stage == "update" {
		return d.err
	}
	return d.DB.Update(func(tx database.Tx) error {
		if err := fn(feeSaveFailureTx{Tx: tx, db: d}); err != nil {
			return err
		}
		if d.stage == "commit" {
			return d.err
		}
		return nil
	})
}

type feeSaveFailureTx struct {
	database.Tx
	db *feeSaveFailureDB
}

func (tx feeSaveFailureTx) Metadata() database.Bucket {
	return feeSaveFailureBucket{shutdownBucket: tx.Tx.Metadata(), db: tx.db}
}

// Use an alias so the embedded interface does not hide its Bucket method.
type shutdownBucket = database.Bucket

type feeSaveFailureBucket struct {
	shutdownBucket
	db *feeSaveFailureDB
}

func (b feeSaveFailureBucket) Put(key, value []byte) error {
	if b.db.stage == "put" {
		return b.db.err
	}
	return b.shutdownBucket.Put(key, value)
}

func TestServerShutdownPreservesFeeEstimatorErrors(t *testing.T) {
	for _, stage := range []string{"success", "update", "put", "commit", "index-close"} {
		t.Run(stage, func(t *testing.T) {
			previousCfg := cfg
			cfg = &config{}
			t.Cleanup(func() { cfg = previousCfg })
			mgr := &assetindexer.IndexerMgr{}
			t.Cleanup(func() { _ = mgr.Close() })
			chainDB, err := database.Create("ffldb", t.TempDir(), chaincfg.TestNetParams.Net)
			require.NoError(t, err)
			t.Cleanup(func() { _ = chainDB.Close() })
			injected := errors.New("injected fee estimator " + stage + " failure")
			indexClosed := false
			mgr.SetRPCShutdown(func() error {
				indexClosed = true
				if stage == "index-close" {
					return injected
				}
				return nil
			})
			rpc := &rpcServer{quit: make(chan int)}
			rpc.ntfnMgr = newWsNotificationManager(rpc)
			rpc.ntfnMgr.wg.Add(1)
			var release sync.Once
			unblock := func() { release.Do(rpc.ntfnMgr.wg.Done) }
			t.Cleanup(unblock)
			s := &server{
				assetIndexer: mgr, quit: make(chan struct{}), rpcServer: rpc,
				db:           &feeSaveFailureDB{DB: chainDB, stage: stage, err: injected},
				feeEstimator: mempool.NewFeeEstimator(1, 1), posMiner: &posminer.POSMiner{},
			}
			firstStopped := make(chan error, 1)
			go func() { firstStopped <- s.Stop() }()
			select {
			case <-rpc.ntfnMgr.quit:
			case <-time.After(2 * time.Second):
				t.Fatal("first Stop did not reach RPC shutdown")
			}
			// Model main entering shutdown while another Stop caller is active.
			require.NoError(t, s.Stop())
			drained := make(chan error, 1)
			go func() { drained <- s.WaitForShutdown() }()
			unblock()
			select {
			case err = <-drained:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown did not finish after a fee persistence failure")
			}
			if stage == "success" {
				require.NoError(t, err)
				require.NoError(t, s.WaitForShutdown())
			} else {
				require.ErrorIs(t, err, injected)
				require.ErrorIs(t, s.WaitForShutdown(), injected)
			}
			feeSaveFailed := stage == "update" || stage == "put" || stage == "commit"
			if feeSaveFailed {
				require.ErrorIs(t, <-firstStopped, injected)
			} else {
				require.NoError(t, <-firstStopped)
			}
			require.NoError(t, chainDB.View(func(tx database.Tx) error {
				value := tx.Metadata().Get(mempool.EstimateFeeDatabaseKey)
				if feeSaveFailed {
					require.Nil(t, value, "failed save must not partially persist fee state")
				} else {
					require.NotEmpty(t, value)
				}
				return nil
			}))
			require.True(t, indexClosed, "a save failure must not skip remaining cleanup")
		})
	}
}

type blockCloseFailureDB struct {
	database.DB
	err error
}

func (d blockCloseFailureDB) Close() error {
	return errors.Join(d.DB.Close(), d.err)
}

func TestCloseBlockDBPreservesErrorsAndClosesDatabase(t *testing.T) {
	priorFailure, closeFailure := errors.New("prior failure"), errors.New("close failure")
	for _, tc := range []struct {
		name  string
		prior error
		close error
	}{
		{"success", nil, nil},
		{"prior", priorFailure, nil},
		{"close", nil, closeFailure},
		{"both", priorFailure, closeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir()
			db, err := database.Create("ffldb", path, chaincfg.TestNetParams.Net)
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			require.NoError(t, db.Update(func(tx database.Tx) error {
				return tx.Metadata().Put([]byte("pending"), []byte("persisted"))
			}))
			err = closeBlockDB(blockCloseFailureDB{DB: db, err: tc.close}, tc.prior)
			if tc.prior == nil && tc.close == nil {
				require.NoError(t, err)
			}
			if tc.prior != nil {
				require.ErrorIs(t, err, tc.prior)
			}
			if tc.close != nil {
				require.ErrorIs(t, err, tc.close)
				require.ErrorContains(t, err, "close block database")
			}
			// Closing must still flush pending metadata, even after a prior error.
			reopened, err := database.Open("ffldb", path, chaincfg.TestNetParams.Net)
			require.NoError(t, err)
			defer reopened.Close()
			require.NoError(t, reopened.View(func(tx database.Tx) error {
				require.Equal(t, "persisted", string(tx.Metadata().Get([]byte("pending"))))
				return nil
			}))
		})
	}
}
