package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCompactAgentStateRoundTripAndRoot(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")
	store := NewRuntimeStore()
	store.Add(runtime)
	root := store.StateRoot()
	raw, err := store.MarshalBinary()
	require.NoError(t, err)
	require.False(t, json.Valid(raw))
	decoded, err := DecodeRuntimeStore(raw)
	require.NoError(t, err)
	require.Equal(t, root, decoded.StateRoot())
	other, ok := decoded.Get(runtime.Address())
	require.True(t, ok)
	require.Equal(t, runtime.State(), other.State())
	require.Equal(t, runtime.managed, other.managed)
	snapshots, err := store.Snapshots()
	require.NoError(t, err)
	jsonBytes, err := json.Marshal(snapshots)
	require.NoError(t, err)
	var fromJSON []RuntimeSnapshot
	require.NoError(t, json.Unmarshal(jsonBytes, &fromJSON))
	old, err := runtimeFromSnapshot(fromJSON[0])
	require.NoError(t, err)
	require.Equal(t, runtime.StateRoot(), old.StateRoot())
	for _, bad := range [][]byte{jsonBytes, raw[:len(raw)-1], append(bytes.Clone(raw), 0)} {
		_, err = DecodeRuntimeStore(bad)
		require.Error(t, err)
	}
	first := runtime.state.Prediction.Bets
	runtime.state.Prediction.Bets = make(map[string]PredictionBetRecord)
	for _, key := range []string{"bob", "alice"} {
		runtime.state.Prediction.Bets[key] = PredictionBetRecord{Address: key, OutcomeID: "a", Amount: "60000"}
	}
	a, err := store.MarshalBinary()
	require.NoError(t, err)
	runtime.state.Prediction.Bets = make(map[string]PredictionBetRecord)
	for _, key := range []string{"alice", "bob"} {
		runtime.state.Prediction.Bets[key] = PredictionBetRecord{Address: key, OutcomeID: "a", Amount: "60000"}
	}
	b, err := store.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, a, b)
	runtime.state.Prediction.Bets = first
}
func decodeAgentJSONBaseline(data []byte) (*RuntimeStore, error) {
	var snapshots []RuntimeSnapshot
	if err := json.Unmarshal(data, &snapshots); err != nil {
		return nil, err
	}
	store := NewRuntimeStore()
	for _, snapshot := range snapshots {
		address, err := DecodeContractAddress(snapshot.Address)
		if err != nil {
			return nil, err
		}
		content, err := snapshot.Contract.Encode()
		if err != nil {
			return nil, err
		}
		deploy := DeployPayload{Type: ContractTypeAgent, SubType: snapshot.Subtype, Version: snapshot.Deploy.AgentVersion, GasLimit: snapshot.Deploy.GasLimit, DeployNonce: snapshot.Deploy.DeployNonce, Flags: snapshot.Deploy.Flags, ContractContent: content}
		if deploy.Version == 0 {
			deploy.Version = snapshot.Version
		}
		runtime, err := NewRuntimeWithDeployer(address, deploy, snapshot.Config, snapshot.Deploy.Deployer)
		if err != nil {
			return nil, err
		}
		if err := snapshot.Managed.Validate(); err != nil {
			return nil, err
		}
		runtime.managed = snapshot.Managed.Clone()
		state, err := json.Marshal(snapshot.State)
		if err != nil {
			return nil, err
		}
		if err := runtime.LoadStateJSON(state); err != nil {
			return nil, err
		}
		if runtimeAddressString(runtime) != snapshot.Address || store.Exists(address) {
			return nil, fmt.Errorf("invalid or duplicate runtime address")
		}
		store.Add(runtime)
	}
	return store, nil
}
func TestCompactAgentStatePerformance(t *testing.T) {
	for _, count := range []int{20, 500, 1001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			runtime := newTestRuntime(t)
			requireReady(t, runtime)
			runtime.state.Prediction.Bets = make(map[string]PredictionBetRecord, count)
			for i := 0; i < count; i++ {
				address := fmt.Sprintf("bettor-%04d", i)
				runtime.state.Prediction.Bets[predictionBetKey(address, "a")] = PredictionBetRecord{Address: address, OutcomeID: "a", Amount: "60000"}
			}
			store := NewRuntimeStore()
			store.Add(runtime)
			raw, err := store.MarshalBinary()
			require.NoError(t, err)
			legacy, err := store.MarshalJSON()
			require.NoError(t, err)
			decoded, err := DecodeRuntimeStore(raw)
			require.NoError(t, err)
			baseline, err := decodeAgentJSONBaseline(legacy)
			require.NoError(t, err)
			require.Equal(t, store.StateRoot(), decoded.StateRoot())
			require.Equal(t, baseline.StateRoot(), decoded.StateRoot())
			for _, codec := range []struct {
				name   string
				encode func() ([]byte, error)
				decode func() error
				size   int
			}{
				{"json", store.MarshalJSON, func() error { _, err := decodeAgentJSONBaseline(legacy); return err }, len(legacy)},
				{"compact", store.MarshalBinary, func() error { _, err := DecodeRuntimeStore(raw); return err }, len(raw)},
			} {
				for _, op := range []string{"encode", "decode"} {
					result := testing.Benchmark(func(b *testing.B) {
						b.ReportAllocs()
						for i := 0; i < b.N; i++ {
							var err error
							if op == "encode" {
								_, err = codec.encode()
							} else {
								err = codec.decode()
							}
							if err != nil {
								b.Fatal(err)
							}
						}
					})
					t.Logf("METRIC bets=%d codec=%s op=%s bytes=%d ns_op=%d B_op=%d allocs_op=%d", count, codec.name, op, codec.size, result.NsPerOp(), result.AllocedBytesPerOp(), result.AllocsPerOp())
				}
			}
		})
	}
}

// JSON exists only as the previous hash algorithm's performance baseline.
func agentRootFixture(runtime *Runtime, compact bool) [32]byte {
	h := sha256.New()
	writeLengthPrefixed(h, []byte(runtime.address.EncodeAddress()))
	writeLengthPrefixed(h, []byte(runtime.deploy.SubType))
	writeUint32(h, runtime.deploy.Version)
	writeLengthPrefixed(h, []byte(runtime.deployer))
	writeUint64(h, runtime.deploy.DeployNonce)
	writeUint32(h, uint32(runtime.deploy.Flags))
	writeLengthPrefixed(h, runtime.deploy.ContractContent)
	if compact {
		e := contractcommon.NewStateEncoder("")
		contractcommon.WriteManagedBalance(e, runtime.managed)
		writeCompactRuntimeState(e, runtime.state)
		raw, err := e.Data()
		if err != nil {
			return [32]byte{}
		}
		h.Write(raw)
	} else {
		managed, err := runtime.managed.MarshalJSON()
		if err != nil {
			return [32]byte{}
		}
		state, err := json.Marshal(runtime.state)
		if err != nil {
			return [32]byte{}
		}
		writeLengthPrefixed(h, managed)
		writeLengthPrefixed(h, state)
	}
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

func TestCompactAgentRootFieldsAndOrder(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")
	root := runtime.StateRoot()
	require.NotEqual(t, [32]byte{}, root)
	require.Equal(t, agentRootFixture(runtime, true), root)
	require.NotEqual(t, agentRootFixture(runtime, false), root)
	original := runtime.State()
	for i := 0; i < 3; i++ {
		require.Equal(t, root, runtime.StateRoot())
	}
	require.Equal(t, original, runtime.State(), "hashing must not change runtime state")
	for _, mutate := range []func(){
		func() { runtime.state.Closed = !runtime.state.Closed },
		func() { runtime.state.Status += "changed" },
		func() { runtime.state.Prediction.Status += "changed" },
		func() { runtime.state.Prediction.GasBalance = "101" },
		func() {
			runtime.state.Prediction.Bets["bob|a"] = PredictionBetRecord{Address: "bob", OutcomeID: "a", Amount: "60000"}
		},
		func() {
			runtime.state.Prediction.Confirmations = append(runtime.state.Prediction.Confirmations, PredictionConfirmRecord{Agent: "agent", OutcomeID: "a", ObservedAt: 101})
		},
		func() {
			runtime.state.Prediction.Rejections = append(runtime.state.Prediction.Rejections, PredictionRejectRecord{Agent: "agent", Reason: "invalid", CheckedAt: 101})
		},
		func() { runtime.managed.Value++ },
	} {
		before := runtime.StateRoot()
		mutate()
		require.NotEqual(t, before, runtime.StateRoot())
		require.Equal(t, agentRootFixture(runtime, true), runtime.StateRoot())
	}
	records := runtime.state.Prediction.Bets
	runtime.state.Prediction.Bets = make(map[string]PredictionBetRecord, len(records))
	keys := contractcommon.SortedStateKeys(records)
	for i := len(keys) - 1; i >= 0; i-- {
		runtime.state.Prediction.Bets[keys[i]] = records[keys[i]]
	}
	ordered := runtime.StateRoot()
	runtime.state.Prediction.Bets = records
	require.Equal(t, ordered, runtime.StateRoot())
}

func TestCompactAgentRootPerformance(t *testing.T) {
	for _, count := range []int{20, 500, 1001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			runtime := newTestRuntime(t)
			requireReady(t, runtime)
			runtime.state.Prediction.Bets = make(map[string]PredictionBetRecord, count)
			for i := 0; i < count; i++ {
				address := fmt.Sprintf("bettor-%04d", i)
				runtime.state.Prediction.Bets[predictionBetKey(address, "a")] = PredictionBetRecord{Address: address, OutcomeID: "a", Amount: "60000"}
			}
			require.Equal(t, agentRootFixture(runtime, true), runtime.StateRoot())
			for _, codec := range []string{"json", "compact"} {
				result := testing.Benchmark(func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						var got [32]byte
						if codec == "json" {
							got = agentRootFixture(runtime, false)
						} else {
							got = runtime.StateRoot()
						}
						if got == ([32]byte{}) {
							b.Fatal("empty root")
						}
					}
				})
				t.Logf("METRIC_ROOT bets=%d codec=%s ns_op=%d B_op=%d allocs_op=%d", count, codec, result.NsPerOp(), result.AllocedBytesPerOp(), result.AllocsPerOp())
			}
		})
	}
}
