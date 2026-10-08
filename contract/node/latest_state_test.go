package node

import (
	"bytes"
	"errors"
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleStateReplacesPreviousSnapshotAtomically(t *testing.T) {
	for _, codec := range defaultModuleStateCodecs() {
		t.Run(codec.Name, func(t *testing.T) {
			db := testEVMStateDB(t)
			defer db.Close()
			old, next := chainhash.Hash{1}, chainhash.Hash{2}
			var bucketName []byte
			var snapshot any
			switch codec.Name {
			case "EVM":
				bucketName, snapshot = evmStateBucketName, evm.NewMemoryStateDB()
			case "template":
				bucketName, snapshot = templateStateBucketName, template.NewRuntimeStore()
			case "agent":
				bucketName, snapshot = agentStateBucketName, agent.NewRuntimeStore()
			}
			// Seed a chunked latest snapshot; no historical migration is needed.
			require.NoError(t, db.Update(func(tx database.Tx) error {
				if err := codec.Store(tx, &old, snapshot); err != nil {
					return err
				}
				bucket := tx.Metadata().Bucket(bucketName).Bucket([]byte("byblock"))
				return putStateBlob(bucket, old[:], bytes.Repeat([]byte{7}, MaxPersistedContractStateBytes+1))
			}))
			rollback := errors.New("rollback chain transaction")
			require.ErrorIs(t, db.Update(func(tx database.Tx) error {
				if err := codec.Store(tx, &next, snapshot); err != nil {
					return err
				}
				return rollback
			}), rollback)
			require.NoError(t, db.View(func(tx database.Tx) error {
				parent := tx.Metadata().Bucket(bucketName)
				bucket := parent.Bucket([]byte("byblock"))
				assert.Equal(t, old[:], parent.Get([]byte("tip")))
				assert.NotNil(t, bucket.Get(old[:]))
				assert.NotNil(t, bucket.Bucket(stateChunkBucketKey(old[:])))
				assert.Nil(t, bucket.Get(next[:]))
				return nil
			}))
			require.NoError(t, db.Update(func(tx database.Tx) error { return codec.Store(tx, &next, snapshot) }))
			require.NoError(t, db.View(func(tx database.Tx) error {
				parent := tx.Metadata().Bucket(bucketName)
				bucket := parent.Bucket([]byte("byblock"))
				assert.Equal(t, next[:], parent.Get([]byte("tip")))
				assert.Nil(t, bucket.Get(old[:]))
				assert.Nil(t, bucket.Bucket(stateChunkBucketKey(old[:])))
				assert.NotNil(t, bucket.Get(next[:]))
				return nil
			}))
		})
	}
}

func TestLatestSparseModuleStateAfterReopen(t *testing.T) {
	path := t.TempDir()
	db, err := database.Create("ffldb", path, wire.TestNet)
	require.NoError(t, err)
	first := btcutilBlockWithPrev(chainhash.Hash{})
	quiet := btcutilBlockWithPrev(*first.Hash())
	changed := btcutilBlockWithPrev(*quiet.Hash())
	last := btcutilBlockWithPrev(*changed.Hash())
	templateState, agentState, evmState := template.NewRuntimeStore(), agent.NewRuntimeStore(), evm.NewMemoryStateDB()
	evmState.SetNonce(gethcommon.Address{1}, 1, 0)
	err = db.Update(func(tx database.Tx) error {
		for _, b := range []*btcutil.Block{first, quiet, changed, last} {
			if err := tx.StoreBlock(b); err != nil {
				return err
			}
		}
		if err := dbStoreTemplateBlockState(tx, first.Hash(), templateState); err != nil {
			return err
		}
		if err := dbStoreAgentBlockState(tx, first.Hash(), agentState); err != nil {
			return err
		}
		if err := dbStoreEVMBlockState(tx, first.Hash(), evmState); err != nil {
			return err
		}
		evmState.SetNonce(gethcommon.Address{1}, 2, 0)
		return dbStoreEVMBlockState(tx, changed.Hash(), evmState)
	})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	require.NoError(t, db.Close())
	db, err = database.Open("ffldb", path, wire.TestNet)
	require.NoError(t, err)
	defer db.Close()
	params := chaincfg.TestNetParams
	params.POSV2Height = 1
	validator := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		ChainParams:       &params,
		TemplateValidator: NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{ChainParams: &params, NewRuntime: NewTemplateStateStore(db).RuntimeFactory()}),
		EVMValidator:      NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{ChainParams: &params, NewRuntime: NewEVMStateStore(db).RuntimeFactory()}),
		AgentValidator:    NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{ChainParams: &params, NewRuntime: NewAgentStateStore(db).RuntimeFactory()}),
	})
	coinbase := testEVMCoinbaseTx()
	want := contract.CombineStateRoots(templateState.StateRoot(), evmState.StateRoot(), agentState.StateRoot())
	require.NoError(t, contract.UpsertCoinbaseStateRoot(coinbase, want))
	block := btcutil.NewBlock(&wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: *last.Hash()}, Transactions: []*wire.MsgTx{coinbase}})
	block.SetHeight(5)
	require.NoError(t, validator.ValidateContractBlock(block, blockchain.NewUtxoViewpoint()))
	for _, registration := range validator.cfg.Modules {
		_, cached := validator.ContractBlockPostState(registration.Descriptor.Type(), block.Hash())
		require.False(t, cached, "unchanged module must not create a new snapshot")
	}
	tip, _, err := NewTemplateStateStore(db).LoadTip()
	require.NoError(t, err)
	require.Equal(t, first.Hash(), tip)
	tip, _, err = NewAgentStateStore(db).LoadTip()
	require.NoError(t, err)
	require.Equal(t, first.Hash(), tip)
	tip, _, err = NewEVMStateStore(db).LoadTip()
	require.NoError(t, err)
	require.Equal(t, changed.Hash(), tip)
}
