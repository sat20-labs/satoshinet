package main

import (
	"fmt"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Reuse the existing canonical POS fixture. The module fixture executes one
// actual funded DEPLOY with the production Agent backend; unrelated chain
// funding/script and RESULT construction are covered by the Agent E2E suite.
type growingAgentExecution struct {
	*node.AgentBlockExecutionValidator
	parent *agent.RuntimeStore
	deploy *wire.MsgTx
}

func growingAgentDescriptor() framework.ModuleDescriptor {
	return framework.ModuleDescriptor{NameValue: "agent", TypeValue: framework.ModuleAgent,
		ClassifyOrder: func(*wire.MsgTx, string) (framework.TxOrderInfo, error) { return framework.TxOrderInfo{}, nil },
		MatchesOrder:  func(framework.TxOrderInfo) bool { return false }}
}
func (v *growingAgentExecution) execute() (*agent.RuntimeStore, error) {
	state := v.parent.Clone()
	exec, err := agent.ExecuteBlock(agent.BlockExecutionRequest{Txs: []*wire.MsgTx{v.deploy}, Store: state,
		ContractPrefix: agent.TestnetContractPrefix, RuntimeConfig: agent.RuntimeConfig{ChainParams: &chaincfg.RegressionNetParams},
		GasConfig: agent.DefaultGasConfig(), BlockHeight: 1,
		ResolveInvoker: func(*wire.MsgTx, contractcommon.Tx) (string, error) { return "deployer", nil }})
	if err != nil {
		return nil, err
	}
	if len(exec.Records) != 1 || exec.Records[0].Status != agent.ResultStatusSuccess || len(exec.ResultPlans) != 1 {
		return nil, fmt.Errorf("ordinary Agent deployment failed or omitted Result gas")
	}
	return state, nil
}
func (v *growingAgentExecution) HasContractBlockActivity(*btcutil.Block, *blockchain.UtxoViewpoint) (bool, error) {
	return true, nil
}
func (v *growingAgentExecution) ContractBlockModule(*btcutil.Block, *blockchain.UtxoViewpoint) (framework.Module, error) {
	return framework.ModuleAdapter{ModuleDescriptor: growingAgentDescriptor(), ExecuteWorkBlockFunc: func(framework.WorkExecutionRequest) (framework.ExecutionResult, error) {
		state, err := v.execute()
		if err != nil {
			return framework.ExecutionResult{}, err
		}
		return framework.ExecutionResult{ModuleType: framework.ModuleAgent, PostState: state, StateRoot: state.StateRoot(), StateChanged: true}, nil
	}, VerifyResultTxsFunc: func(framework.ResultVerifyRequest, framework.ExecutionResult) error { return nil }}, nil
}

func TestAgentDeploymentCrossesStateChunkBoundaryDuringPOSApproval(t *testing.T) {
	prediction := agent.PredictionContract{Subtype: agent.SubtypePrediction, Title: "prediction",
		Description: strings.Repeat("p", 380), TimeBase: agent.TimeBaseHeight, EventTime: 29,
		BetDeadline: 10, ConfirmAfter: 30, SourceURL: "https://example.com/match", BetAsset: agent.SatoshiAssetName,
		MinBetUnit: "1000", Outcomes: []agent.PredictionOutcome{{ID: "a", Text: "home"}, {ID: "b", Text: "away"}}}
	require.NoError(t, prediction.Check())
	content, err := prediction.Encode()
	require.NoError(t, err)
	payload := agent.DeployPayload{Type: agent.ContractTypeAgent, SubType: agent.SubtypePrediction,
		Version: agent.CurrentAgentVersion, GasLimit: agent.DefaultGasConfig().DeployBaseGas, ContractContent: content}
	parent := agent.NewRuntimeStore()
	add := func(nonce uint64) agent.ContractAddress {
		payload.DeployNonce = nonce
		addr, _, err := agent.DeriveContractAddress(agent.TestnetContractPrefix, payload.SubType, content, "deployer", nonce)
		require.NoError(t, err)
		runtime, err := agent.NewRuntimeWithDeployer(addr, payload, agent.RuntimeConfig{ChainParams: &chaincfg.RegressionNetParams}, "deployer")
		require.NoError(t, err)
		parent.Add(runtime)
		return addr
	}
	add(10000)
	one, err := parent.MarshalBinary()
	require.NoError(t, err)
	// Equal-length nonces and addresses make the JSON record size predictable.
	count := (node.MaxPersistedContractStateBytes - 1) / (len(one) - 1)
	for i := 1; i < count; i++ {
		add(uint64(10000 + i))
	}
	before, err := parent.MarshalBinary()
	require.NoError(t, err)
	require.LessOrEqual(t, len(before), node.MaxPersistedContractStateBytes)
	payload.DeployNonce = uint64(10000 + count)
	addr, _, err := agent.DeriveContractAddress(agent.TestnetContractPrefix, payload.SubType, content, "deployer", payload.DeployNonce)
	require.NoError(t, err)
	script, err := agent.DeployNullDataScript(payload)
	require.NoError(t, err)
	outputScript, err := agent.ContractPkScript(addr)
	require.NoError(t, err)
	deploy := wire.NewMsgTx(2)
	deploy.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{7}}, nil, nil))
	deploy.AddTxOut(wire.NewTxOut(0, nil, script))
	gasAssets := wire.TxAssets{{Name: *wire.NewAssetNameFromString(agent.DefaultGasConfig().GasAssetName), Amount: *indexer.NewDefaultDecimal(500)}}
	deploy.AddTxOut(wire.NewTxOut(0, gasAssets, outputScript))
	_, err = agent.ValidateDeployTxBasicWithActor(deploy, agent.TestnetContractPrefix, agent.RuntimeConfig{ChainParams: &chaincfg.RegressionNetParams}, agent.DefaultGasConfig(), "deployer")
	require.NoError(t, err)
	execution := &growingAgentExecution{AgentBlockExecutionValidator: node.NewAgentBlockExecutionValidator(node.AgentBlockExecutionConfig{}), parent: parent, deploy: deploy}
	expected, err := execution.execute()
	require.NoError(t, err)
	after, err := expected.MarshalBinary()
	require.NoError(t, err)
	require.Greater(t, len(after), node.MaxPersistedContractStateBytes)
	require.Less(t, len(after)-len(before), len(one)+1024, "one small deployment crosses the boundary")
	provider := node.NewCompositeContractBlockValidator(node.CompositeContractBlockValidatorConfig{ChainParams: &chaincfg.RegressionNetParams,
		Modules: []node.RegisteredContractValidator{{Descriptor: growingAgentDescriptor(), Validator: execution}}})
	manager := node.NewContractStateManager()
	rpc, db, key, pub, candidate := publicationChain(t, provider, manager)
	genesis := rpc.cfg.ChainParams.GenesisHash
	store := node.NewAgentStateStore(db)
	require.NoError(t, store.StoreBlockState(genesis, parent))
	roots := framework.NewStateSet()
	roots.SetRoot(framework.ModuleAgent, expected.StateRoot())
	require.NoError(t, contractcommon.UpsertCoinbaseStateRoot(candidate.Transactions[0], roots.CombinedRoot()))
	refreshPublicationCommitment(candidate)
	require.NoError(t, rpc.cfg.Chain.CheckConnectBlockTemplate(btcutil.NewBlock(candidate.Copy())))
	_, err = rpc.cfg.Chain.ApprovePOSBlock(candidate, pub, func(message []byte) ([]byte, error) {
		return ecdsa.Sign(key, chainhash.HashB(message)).Serialize(), nil
	})
	require.NoError(t, err, "one ordinary deployment must not latch a POS storage failure")
	require.Equal(t, int32(1), rpc.cfg.Chain.BestSnapshot().Height)
	hash := candidate.BlockHash()
	require.NoError(t, db.Close())
	db.DB, err = database.Open("ffldb", db.path, rpc.cfg.ChainParams.Net)
	require.NoError(t, err)
	loaded, err := store.LoadBlockState(&hash)
	require.NoError(t, err)
	require.Equal(t, expected.StateRoot(), loaded.StateRoot())
	tip, tipState, err := store.LoadTip()
	require.NoError(t, err)
	require.Equal(t, hash, *tip)
	require.Equal(t, expected.StateRoot(), tipState.StateRoot())
	chunksKey := append(append([]byte(nil), hash[:]...), []byte(":chunks")...)
	require.NoError(t, db.View(func(tx database.Tx) error {
		bucket := tx.Metadata().Bucket([]byte("agentstate")).Bucket([]byte("byblock"))
		chunks := bucket.Bucket(chunksKey)
		require.NotNil(t, chunks)
		return chunks.ForEach(func(_ []byte, value []byte) error {
			require.LessOrEqual(t, len(value), node.MaxPersistedContractStateBytes)
			return nil
		})
	}))
	// Replacement cleans obsolete chunks. Only the latest state is retained;
	// deleting it cannot restore a pruned parent snapshot.
	_, err = store.LoadBlockState(genesis)
	require.ErrorIs(t, err, node.ErrAgentStateNotFound)
	require.NoError(t, store.StoreBlockState(&hash, agent.NewRuntimeStore()))
	require.NoError(t, db.View(func(tx database.Tx) error {
		require.Nil(t, tx.Metadata().Bucket([]byte("agentstate")).Bucket([]byte("byblock")).Bucket(chunksKey))
		return nil
	}))
	require.NoError(t, store.StoreBlockState(&hash, expected))
	require.NoError(t, db.Update(func(tx database.Tx) error { return manager.DeleteContractBlockState(tx, &hash, genesis) }))
	_, err = store.LoadBlockState(&hash)
	require.ErrorIs(t, err, node.ErrAgentStateNotFound)
	tip, tipState, err = store.LoadTip()
	require.NoError(t, err)
	require.Nil(t, tip)
	require.Equal(t, agent.NewRuntimeStore().StateRoot(), tipState.StateRoot())
	require.NoError(t, db.View(func(tx database.Tx) error {
		require.Nil(t, tx.Metadata().Bucket([]byte("agentstate")).Bucket([]byte("byblock")).Bucket(chunksKey))
		return nil
	}))
	require.NoError(t, store.StoreBlockState(&hash, expected))
	require.NoError(t, db.Update(func(tx database.Tx) error {
		return tx.Metadata().Bucket([]byte("agentstate")).Bucket([]byte("byblock")).Bucket(chunksKey).Delete(make([]byte, 8))
	}))
	_, err = store.LoadBlockState(&hash)
	require.Error(t, err, "missing chunks must fail closed")
}
