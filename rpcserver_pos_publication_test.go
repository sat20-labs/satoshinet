package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/blockchain/indexers"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/database"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type publicationDB struct {
	database.DB
	path             string
	entered, release chan struct{}
	fail             bool
	gate             sync.Once
}

func (d *publicationDB) Sync() error {
	if d.entered != nil {
		d.gate.Do(func() { close(d.entered); <-d.release })
	}
	if d.fail {
		return errors.New("injected canonical Sync failure")
	}
	return d.DB.(interface{ Sync() error }).Sync()
}

type publicationReadiness struct {
	seq     *scommon.MiningSequenceMgr
	genesis chainhash.Hash
}

func (r *publicationReadiness) GetSeqMgr() *scommon.MiningSequenceMgr { return r.seq }
func (r *publicationReadiness) GetInternalTip() (int, chainhash.Hash, bool) {
	return 0, r.genesis, true
}
func (r *publicationReadiness) InternalTipReady(h int, hash *chainhash.Hash) bool {
	return h == 0 && hash != nil && *hash == r.genesis
}
func (r *publicationReadiness) EnsureInternalTip(h int, hash *chainhash.Hash, _ int) error {
	if !r.InternalTipReady(h, hash) {
		return errors.New("unexpected parent")
	}
	return nil
}
func (r *publicationReadiness) WaitForInternalTip(h int, hash *chainhash.Hash, _ <-chan struct{}) error {
	return r.EnsureInternalTip(h, hash, h)
}

func publicationChain(t *testing.T, contractValidator blockchain.ContractBlockValidator, stateManager blockchain.ContractStateManager) (*rpcServer, *publicationDB, *btcec.PrivateKey, string, *wire.MsgBlock) {
	t.Helper()
	params := chaincfg.RegressionNetParams
	genesis := params.GenesisBlock.BlockHash()
	params.GenesisHash = &genesis
	params.Bech32HRPSegwit = chaincfg.TestNetParams.Bech32HRPSegwit
	params.POSV2Height = 1
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: params.GenesisHash}}
	path := t.TempDir()
	raw, err := database.Create("ffldb", path, params.Net)
	require.NoError(t, err)
	db := &publicationDB{DB: raw, path: path}
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	pub := hex.EncodeToString(key.PubKey().SerializeCompressed())
	seq := scommon.NewMiningSequenceMgr(&params)
	require.NoError(t, seq.Init(map[string]*scommon.CoreNodeInfo{pub: scommon.NewCoreNodeInfo(nil)}, 0, ""))
	readiness := &publicationReadiness{seq: seq, genesis: *params.GenesisHash}
	txIndex := indexers.NewTxIndex(db)
	addrIndex := indexers.NewAddrIndex(db, &params)
	chain, err := blockchain.New(&blockchain.Config{DB: db, ChainParams: &params, TimeSource: blockchain.NewMedianTime(), AssetIndexReadiness: readiness, IndexManager: indexers.NewManager(db, []indexers.Indexer{txIndex, addrIndex}), ContractBlockValidator: contractValidator, ContractStateManager: stateManager})
	require.NoError(t, err)
	cb := wire.NewMsgTx(1)
	script, err := txscript.NewScriptBuilder().AddInt64(1).AddInt64(0).AddData(ecdsa.Sign(key, chainhash.HashB(scommon.GetScriptSignData(1, 0))).Serialize()).Script()
	require.NoError(t, err)
	cb.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: ^uint32(0)}, script, wire.TxWitness{make([]byte, 32)}))
	addr, err := btcutil.DecodeAddress(seq.GetCurrentMiningAddr(), &params)
	require.NoError(t, err)
	payout, err := txscript.PayToAddrScript(addr)
	require.NoError(t, err)
	cb.AddTxOut(wire.NewTxOut(blockchain.CalcBlockSubsidy(1, &params), nil, payout))
	block := &wire.MsgBlock{Header: wire.BlockHeader{Version: 4, PrevBlock: *params.GenesisHash, Bits: params.PowLimitBits, Timestamp: time.Unix(time.Now().Unix(), 0)}, Transactions: []*wire.MsgTx{cb}}
	refreshPublicationCommitment(block)
	return &rpcServer{cfg: rpcserverConfig{DB: db, Chain: chain, ChainParams: &params, TxIndex: txIndex, AddrIndex: addrIndex}}, db, key, pub, block
}
func refreshPublicationCommitment(block *wire.MsgBlock) {
	cb := block.Transactions[0]
	if n := len(cb.TxOut); n > 0 && bytes.HasPrefix(cb.TxOut[n-1].PkScript, blockchain.WitnessMagicBytes) {
		cb.TxOut = cb.TxOut[:n-1]
	}
	root := blockchain.CalcMerkleRoot(btcutil.NewBlock(block).Transactions(), true)
	var preimage [64]byte
	copy(preimage[:32], root[:])
	cb.AddTxOut(wire.NewTxOut(0, nil, append(append([]byte(nil), blockchain.WitnessMagicBytes...), chainhash.DoubleHashB(preimage[:])...)))
	block.Header.MerkleRoot = blockchain.CalcMerkleRoot(btcutil.NewBlock(block).Transactions(), false)
}

func TestSearchRawTransactionsWaitsForCanonicalSync(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			rpc, db, key, pub, block := publicationChain(t, nil, nil)
			db.entered, db.release, db.fail = make(chan struct{}), make(chan struct{}), fail
			var once sync.Once
			release := func() { once.Do(func() { close(db.release) }) }
			defer release()
			approved := make(chan error, 1)
			go func() {
				_, err := rpc.cfg.Chain.ApprovePOSBlock(block, pub, func(msg []byte) ([]byte, error) { return ecdsa.Sign(key, chainhash.HashB(msg)).Serialize(), nil })
				approved <- err
			}()
			select {
			case <-db.entered:
			case err := <-approved:
				t.Fatalf("approval before Sync: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("Sync not reached")
			}
			_, addresses, _, err := txscript.ExtractPkScriptAddrs(block.Transactions[0].TxOut[0].PkScript, rpc.cfg.ChainParams)
			require.NoError(t, err)
			verbose, count := 0, 1
			type response struct {
				value interface{}
				err   error
			}
			result := make(chan response, 1)
			go func() {
				v, err := handleSearchRawTransactions(rpc, &btcjson.SearchRawTransactionsCmd{Address: addresses[0].EncodeAddress(), Verbose: &verbose, Count: &count}, nil)
				result <- response{v, err}
			}()
			var early *response
			select {
			case got := <-result:
				early = &got
				t.Error("approved witness returned before canonical Sync")
			case <-time.After(100 * time.Millisecond):
			}
			release()
			if fail {
				require.Error(t, <-approved)
			} else {
				require.NoError(t, <-approved)
			}
			var got response
			if early != nil {
				got = *early
			} else {
				select {
				case got = <-result:
				case <-time.After(3 * time.Second):
					t.Fatal("RPC blocked after Sync")
				}
			}
			if fail {
				require.Error(t, got.err)
				require.Nil(t, got.value)
			} else {
				require.NoError(t, got.err)
				rows := got.value.([]string)
				require.Len(t, rows, 1)
				raw, err := hex.DecodeString(rows[0])
				require.NoError(t, err)
				tx := wire.NewMsgTx(1)
				require.NoError(t, tx.Deserialize(bytes.NewReader(raw)))
				require.Len(t, tx.TxIn[0].Witness, 2)
			}
		})
	}
}

func TestChainRecoveryRPCAccessRequiresAdmin(t *testing.T) {
	for _, method := range []string{"invalidateblock", "reconsiderblock"} {
		previous := rpcHandlers[method]
		rpcHandlers[method] = func(*rpcServer, interface{}, <-chan struct{}) (interface{}, error) { return true, nil }
		t.Run(method, func(t *testing.T) {
			request := &btcjson.Request{Jsonrpc: btcjson.RpcVersion1, ID: 1, Method: method, Params: []json.RawMessage{json.RawMessage(`"0000000000000000000000000000000000000000000000000000000000000000"`)}}
			var limited, admin btcjson.Response
			rpc := &rpcServer{}
			require.NoError(t, json.Unmarshal(rpc.processRequest(request, false, nil), &limited))
			require.NotNil(t, limited.Error)
			require.Contains(t, limited.Error.Message, "not authorized")
			require.NoError(t, json.Unmarshal(rpc.processRequest(request, true, nil), &admin))
			require.Nil(t, admin.Error)
			require.Equal(t, json.RawMessage(`true`), admin.Result)
		})
		rpcHandlers[method] = previous
	}
}

// Use the production coordinator, EVM recorder, codec and durable state store.
// A block module fixture runs actual EVM CREATE without introducing funding
// transactions unrelated to this candidate-cache lifecycle regression.
type candidateExecutionFixture struct {
	*node.EVMBlockExecutionValidator
	calls     int
	dropState bool
}

func candidateModuleDescriptor() framework.ModuleDescriptor {
	return framework.ModuleDescriptor{NameValue: "evm", TypeValue: framework.ModuleEVM,
		ClassifyOrder: func(*wire.MsgTx, string) (framework.TxOrderInfo, error) { return framework.TxOrderInfo{}, nil },
		MatchesOrder:  func(framework.TxOrderInfo) bool { return false }}
}
func executeCandidateContract(block *btcutil.Block) (*evm.MemoryStateDB, error) {
	runtime := evm.NewRuntime(nil)
	code := []byte{0x60, 0x01, 0x60, 0x0c, 0x60, 0x00, 0x39, 0x60, 0x01, 0x60, 0x00, 0xf3, 0x00}
	result := runtime.Deploy(evm.DeployRequest{CallerAddress: "11112233445566778899aabbccddeeff00112233", CallID: "candidate-deploy", InitCode: code, Gas: 200000, DeployNonce: 3, Block: evm.BlockContext{Number: uint64(block.Height()), Time: uint64(block.MsgBlock().Header.Timestamp.Unix()), GasLimit: 1000000, FixedGasPrice: 1}})
	if result.Err != nil {
		return nil, result.Err
	}
	if len(result.RuntimeCode) == 0 {
		return nil, fmt.Errorf("deployment produced no code")
	}
	return runtime.State, nil
}
func (v *candidateExecutionFixture) HasContractBlockActivity(*btcutil.Block, *blockchain.UtxoViewpoint) (bool, error) {
	return true, nil
}
func (v *candidateExecutionFixture) ContractBlockModule(block *btcutil.Block, _ *blockchain.UtxoViewpoint) (framework.Module, error) {
	return framework.ModuleAdapter{ModuleDescriptor: candidateModuleDescriptor(), ExecuteWorkBlockFunc: func(framework.WorkExecutionRequest) (framework.ExecutionResult, error) {
		v.calls++
		state, err := executeCandidateContract(block)
		if err != nil {
			return framework.ExecutionResult{}, err
		}
		return framework.ExecutionResult{ModuleType: framework.ModuleEVM, PostState: state, StateRoot: state.StateRoot(), StateChanged: true}, nil
	}, VerifyResultTxsFunc: func(framework.ResultVerifyRequest, framework.ExecutionResult) error { return nil }}, nil
}
func (v *candidateExecutionFixture) RecordContractBlockState(block *btcutil.Block, exec framework.ExecutionResult) error {
	if v.dropState {
		return nil
	}
	return v.EVMBlockExecutionValidator.RecordContractBlockState(block, exec)
}
func TestSameHashProposalPersistsReexecutedContractState(t *testing.T) {
	for _, dropState := range []bool{false, true} {
		t.Run(map[bool]string{false: "reexecute", true: "missing-required-state"}[dropState], func(t *testing.T) {
			execution := &candidateExecutionFixture{EVMBlockExecutionValidator: node.NewEVMBlockExecutionValidator(node.EVMBlockExecutionConfig{}), dropState: dropState}
			provider := node.NewCompositeContractBlockValidator(node.CompositeContractBlockValidatorConfig{ChainParams: &chaincfg.RegressionNetParams, Modules: []node.RegisteredContractValidator{{Descriptor: candidateModuleDescriptor(), Validator: execution}}})
			rpc, db, key, _, candidate := publicationChain(t, provider, node.NewContractStateManager())
			block := btcutil.NewBlock(candidate)
			block.SetHeight(1)
			expected, err := executeCandidateContract(block)
			require.NoError(t, err)
			roots := framework.NewStateSet()
			roots.SetRoot(framework.ModuleEVM, expected.StateRoot())
			require.NoError(t, contractcommon.UpsertCoinbaseStateRoot(candidate.Transactions[0], roots.CombinedRoot()))
			refreshPublicationCommitment(candidate)
			err = rpc.cfg.Chain.CheckConnectBlockTemplate(btcutil.NewBlock(candidate.Copy()))
			if dropState {
				require.ErrorContains(t, err, "missing required evm post-state")
				require.Equal(t, int32(0), rpc.cfg.Chain.BestSnapshot().Height)
				return
			}
			require.NoError(t, err)
			hash := candidate.BlockHash()
			_, ok := provider.ContractBlockPostState(framework.ModuleEVM, &hash)
			require.True(t, ok)
			bad := candidate.Copy()
			bad.Transactions[0].TxIn[0].Witness = append(bad.Transactions[0].TxIn[0].Witness, []byte{1})
			require.Equal(t, hash, bad.BlockHash())
			require.Error(t, rpc.cfg.Chain.CheckConnectBlockTemplate(btcutil.NewBlock(bad)))
			_, ok = provider.ContractBlockPostState(framework.ModuleEVM, &hash)
			require.False(t, ok)
			approved := candidate.Copy()
			sig := ecdsa.Sign(key, chainhash.HashB(scommon.POSApprovalMessage(rpc.cfg.ChainParams.Net, 1, hash))).Serialize()
			approved.Transactions[0].TxIn[0].Witness = append(approved.Transactions[0].TxIn[0].Witness, sig)
			main, orphan, err := rpc.cfg.Chain.ProcessBlock(btcutil.NewBlock(approved), blockchain.BFFastAdd)
			require.NoError(t, err)
			require.True(t, main)
			require.False(t, orphan)
			require.Equal(t, 2, execution.calls)
			stored, err := node.NewEVMStateStore(db).LoadBlockState(&hash)
			require.NoError(t, err)
			require.Equal(t, expected.StateRoot(), stored.StateRoot())
			require.NoError(t, db.Close())
			db.DB, err = database.Open("ffldb", db.path, rpc.cfg.ChainParams.Net)
			require.NoError(t, err)
			stored, err = node.NewEVMStateStore(db).LoadBlockState(&hash)
			require.NoError(t, err)
			require.Equal(t, expected.StateRoot(), stored.StateRoot())
		})
	}
}
