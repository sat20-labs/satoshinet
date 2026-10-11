package node

import (
	gethcommon "github.com/ethereum/go-ethereum/common"
	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestCompactContractStateColdRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain")
	db, err := database.Create("ffldb", path, wire.TestNet)
	require.NoError(t, err)
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	hash := chainhash.Hash{1, 2, 3}
	// Write a funded template, a ready prediction with a bet, and EVM storage
	// through the real DB stores, then close/reopen the database from disk.
	tc := template.NewAMMContract("ordx:f:test", "100", 20, "2000")
	content, err := tc.Encode()
	require.NoError(t, err)
	ta, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1, template.ContractTypeTemplate, make([]byte, 32))
	require.NoError(t, err)
	tr, err := template.NewRuntimeWithDeployer(ta, template.DeployPayload{SubType: tc.TemplateName(), Version: tc.Version(), Flags: contractcommon.ContractFlagNonClosable, ContractContent: content}, nil, "deployer")
	require.NoError(t, err)
	require.NoError(t, tr.ApplyFunding(contractframework.ContractOutputFromFunding(contractcommon.FundingOutput{Contract: ta, Value: 20, Assets: wire.TxAssets{{Name: wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "test"}, Amount: *scommon.NewDefaultDecimal(100)}}}), ""))
	templates := template.NewRuntimeStore()
	templates.Add(tr)
	templateManaged, ok := templates.ManagedBalance(ta)
	require.True(t, ok)
	require.NoError(t, templateManaged.Credit(20, wire.TxAssets{{Name: wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "test"}, Amount: *scommon.NewDefaultDecimal(100)}}))
	templateState, err := tr.RuntimeState()
	require.NoError(t, err)
	templateRoot := templates.StateRoot()
	require.NoError(t, NewTemplateStateStore(db).StoreBlockState(&hash, templates))
	pc := agent.PredictionContract{Subtype: agent.SubtypePrediction, Title: "restart", Description: "cold restart", TimeBase: agent.TimeBaseHeight, EventTime: 100, BetDeadline: 99, ConfirmAfter: 101, SourceURL: "https://example.com/result", BetAsset: agent.SatoshiAssetName, MinBetUnit: "10000", Outcomes: []agent.PredictionOutcome{{ID: "a", Text: "yes"}, {ID: "b", Text: "no"}}}
	content, err = pc.Encode()
	require.NoError(t, err)
	aa, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1, agent.ContractTypeAgent, make([]byte, 32))
	require.NoError(t, err)
	ar, err := agent.NewRuntimeWithDeployer(aa, agent.DeployPayload{SubType: agent.SubtypePrediction, Version: agent.CurrentAgentVersion, GasLimit: 1000, ContractContent: content}, agent.RuntimeConfig{CoreNodeAddress: "core"}, "deployer")
	require.NoError(t, err)
	require.NoError(t, ar.ApplyReady(agent.ApplyReadyRequest{Invoker: "core"}))
	require.NoError(t, ar.ApplyBet(agent.ApplyBetRequest{Invoker: "alice", Param: agent.PredictionBetParam{OutcomeID: "a"}, AssetName: agent.SatoshiAssetName, Amount: "60000", GasAmount: "100", TimeValue: 1}))
	agents := agent.NewRuntimeStore()
	agents.Add(ar)
	managed, ok := agents.ManagedBalance(aa)
	require.True(t, ok)
	require.NoError(t, managed.Credit(60000, nil))
	agentRoot := agents.StateRoot()
	require.NoError(t, NewAgentStateStore(db).StoreBlockState(&hash, agents))
	es := evm.NewMemoryStateDB()
	addr := gethcommon.Address{1}
	slot := gethcommon.Hash{2}
	value := gethcommon.Hash{3}
	es.SetCode(addr, []byte{0x60, 0x2a}, 0)
	es.SetState(addr, slot, value)
	require.NoError(t, es.RegisterContractDeployment(addr, "owner", 0))
	ea, err := evm.NewContractAddress(evm.TestnetContractPrefix, 1, evm.ContractTypeEVM, evm.EVMAddress(addr))
	require.NoError(t, err)
	eb, ok := es.ManagedBalance(ea)
	require.True(t, ok)
	require.NoError(t, eb.Credit(20000, wire.TxAssets{{Name: wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "test"}, Amount: *scommon.NewDecimal(100, 6), BindingSat: 2}}))
	evmRoot := es.StateRoot()
	require.NoError(t, NewEVMStateStore(db).StoreBlockState(&hash, es))
	require.NoError(t, db.Close())
	db = nil
	db, err = database.Open("ffldb", path, wire.TestNet)
	require.NoError(t, err)
	th, ts, err := NewTemplateStateStore(db).LoadTip()
	require.NoError(t, err)
	require.Equal(t, hash, *th)
	require.Equal(t, templateRoot, ts.StateRoot())
	restoredTemplate, ok := ts.Get(ta)
	require.True(t, ok)
	require.Equal(t, contractcommon.ContractFlagNonClosable, restoredTemplate.DeploymentFlags())
	restoredTemplateState, err := restoredTemplate.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, templateState, restoredTemplateState)
	restoredTemplateManaged, ok := ts.ManagedBalance(ta)
	require.True(t, ok)
	require.Equal(t, *templateManaged, *restoredTemplateManaged, "compare restored backing directly as well as its root")
	ah, as, err := NewAgentStateStore(db).LoadTip()
	require.NoError(t, err)
	require.Equal(t, hash, *ah)
	require.Equal(t, agentRoot, as.StateRoot())
	restored, ok := as.Get(aa)
	require.True(t, ok)
	require.Equal(t, ar.State(), restored.State())
	eh, evs, err := NewEVMStateStore(db).LoadTip()
	require.NoError(t, err)
	require.Equal(t, hash, *eh)
	require.Equal(t, evmRoot, evs.StateRoot())
	require.Equal(t, value, evs.GetState(addr, slot))
}
