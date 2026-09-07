package node

import (
	"bytes"
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/stretchr/testify/require"
)

func reviewTemplateStore(t *testing.T) *template.RuntimeStore {
	t.Helper()
	address, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1, contractcommon.ContractTypeTemplate, bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)
	contract := template.NewLimitOrderContract("ordx:f:review")
	content, err := contract.Encode()
	require.NoError(t, err)
	runtime, err := template.NewRuntime(address, contractcommon.DeployPayload{Type: contractcommon.ContractTypeTemplate, SubType: contract.TemplateName(), Version: contract.Version(), ContractContent: content}, nil)
	require.NoError(t, err)
	runtime.SetState("review-history", bytes.Repeat([]byte{7}, MaxPersistedContractStateBytes))
	store := template.NewRuntimeStore()
	store.Add(runtime)
	return store
}

func TestReviewTemplateLargeStateAndLegacyEncoding(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()
	store := NewTemplateStateStore(db)
	hash := chainhash.Hash{1}
	large := reviewTemplateStore(t)
	require.NoError(t, store.StoreBlockState(&hash, large))
	loaded, err := store.LoadBlockState(&hash)
	require.NoError(t, err)
	require.Equal(t, large.StateRoot(), loaded.StateRoot())
	small := template.NewRuntimeStore()
	require.NoError(t, store.StoreBlockState(&hash, small))
	legacy, err := small.MarshalBinary()
	require.NoError(t, err)
	require.NoError(t, db.View(func(tx database.Tx) error {
		bucket := tx.Metadata().Bucket(templateStateBucketName).Bucket(templateStateByBlockBucketName)
		require.Equal(t, legacy, bucket.Get(hash[:]), "small snapshots must retain the legacy encoding")
		require.Nil(t, bucket.Bucket(stateChunkBucketKey(hash[:])))
		return nil
	}))
	require.NoError(t, store.StoreBlockState(&hash, large))
	require.NoError(t, db.Update(func(tx database.Tx) error {
		chunks := tx.Metadata().Bucket(templateStateBucketName).Bucket(templateStateByBlockBucketName).Bucket(stateChunkBucketKey(hash[:]))
		return chunks.Delete(make([]byte, 8))
	}))
	_, err = store.LoadBlockState(&hash)
	require.Error(t, err, "incomplete snapshots must never decode as empty state")
}

func TestReviewGrowingEVMStatePersists(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()
	store := NewEVMStateStore(db)
	state := evm.NewMemoryStateDB()
	// Many individually valid contracts can exceed the old whole-module limit.
	for i := 0; i < 800; i++ {
		state.SetCode(gethcommon.BytesToAddress([]byte{byte(i >> 8), byte(i)}), make([]byte, 24576), 0)
	}
	hash := chainhash.Hash{9}
	require.NoError(t, store.StoreBlockState(&hash, state))
	loaded, err := store.LoadBlockState(&hash)
	require.NoError(t, err)
	require.Equal(t, state.StateRoot(), loaded.StateRoot())
	require.NoError(t, store.DeleteBlockState(&hash))
	_, err = store.LoadBlockState(&hash)
	require.ErrorIs(t, err, ErrEVMStateNotFound)
}

func TestReviewParentStateNeverUsesUnrelatedTip(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()
	tip := chainhash.Hash{9}
	missing := chainhash.Hash{8}
	evmStore := NewEVMStateStore(db)
	state := evm.NewMemoryStateDB()
	state.SetNonce(gethcommon.Address{1}, 9, 0)
	require.NoError(t, evmStore.StoreBlockState(&tip, state))
	_, err := evmStore.RuntimeFactory()(btcutilBlockWithPrev(missing), nil)
	require.Error(t, err, "unknown parent must not inherit an unrelated tip")
	templateStore := NewTemplateStateStore(db)
	require.NoError(t, templateStore.StoreBlockState(&tip, template.NewRuntimeStore()))
	_, err = templateStore.RuntimeFactory()(btcutilBlockWithPrev(missing), nil)
	require.Error(t, err, "template must fail closed for unknown parent")
}

func TestReviewSparseParentStateUsesItsOwnAncestor(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()
	ancestor := btcutilBlockWithPrev(chainhash.Hash{})
	parent := btcutilBlockWithPrev(*ancestor.Hash())
	require.NoError(t, db.Update(func(tx database.Tx) error {
		if err := tx.StoreBlock(ancestor); err != nil {
			return err
		}
		return tx.StoreBlock(parent)
	}))
	store := NewEVMStateStore(db)
	original := evm.NewMemoryStateDB()
	original.SetNonce(gethcommon.Address{1}, 1, 0)
	require.NoError(t, store.StoreBlockState(ancestor.Hash(), original))
	future := evm.NewMemoryStateDB()
	future.SetNonce(gethcommon.Address{1}, 2, 0)
	require.NoError(t, store.StoreBlockState(&chainhash.Hash{99}, future))
	runtime, err := store.RuntimeFactory()(btcutilBlockWithPrev(*parent.Hash()), nil)
	require.NoError(t, err)
	require.Equal(t, original.StateRoot(), runtime.State.StateRoot())
}

func TestReviewSparseParentStateUsesValidatedAncestor(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()
	ancestor := btcutilBlockWithPrev(chainhash.Hash{})
	parent := btcutilBlockWithPrev(*ancestor.Hash())
	require.NoError(t, db.Update(func(tx database.Tx) error {
		if err := tx.StoreBlock(ancestor); err != nil {
			return err
		}
		return tx.StoreBlock(parent)
	}))
	module, err := NewEVMBlockValidator(Config{DB: db,
		EVMContractUTXOs:    func(contractcommon.ContractAddress) ([]ContractUTXO, error) { return nil, nil },
		EVMResolveRecipient: func([]byte) (string, bool, error) { return "", false, nil },
		AssetPrecision:      func(string) (int, bool) { return 8, true },
	})
	require.NoError(t, err)
	validator := module.(*EVMBlockExecutionValidator)
	state := evm.NewMemoryStateDB()
	state.SetNonce(gethcommon.Address{1}, 42, 0)
	validator.rememberPostState(ancestor.Hash(), state)
	got, err := validator.runtime(btcutilBlockWithPrev(*parent.Hash()), nil)
	require.NoError(t, err)
	require.Equal(t, state.StateRoot(), got.State.StateRoot())
}

func TestReviewProductionEVMBlockHashes(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()
	ancestor := btcutilBlockWithPrev(chainhash.Hash{})
	parent := btcutilBlockWithPrev(*ancestor.Hash())
	require.NoError(t, db.Update(func(tx database.Tx) error {
		if err := tx.StoreBlock(ancestor); err != nil {
			return err
		}
		return tx.StoreBlock(parent)
	}))
	module, err := NewEVMBlockValidator(Config{DB: db,
		EVMContractUTXOs:    func(contractcommon.ContractAddress) ([]ContractUTXO, error) { return nil, nil },
		EVMResolveRecipient: func([]byte) (string, bool, error) { return "", false, nil },
		AssetPrecision:      func(string) (int, bool) { return 8, true },
	})
	require.NoError(t, err)
	block := btcutilBlockWithPrev(*parent.Hash())
	block.SetHeight(2)
	ctx, err := module.(*EVMBlockExecutionValidator).blockContext(block)
	require.NoError(t, err)
	require.Equal(t, [32]byte(*ancestor.Hash()), ctx.BlockHashes[0])
	require.Equal(t, [32]byte(*parent.Hash()), ctx.BlockHashes[1])
}
