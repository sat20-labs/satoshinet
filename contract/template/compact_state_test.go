package template

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

// The previous JSON root is a measurement baseline only, not production code.
func legacyTemplateJSONRoot(runtime *ContractRuntime, state TemplateRuntimeState) [32]byte {
	h := sha256.New()
	base := runtime.base
	writeLengthPrefixed(h, []byte(base.address.EncodeAddress()))
	writeLengthPrefixed(h, []byte(base.templateName))
	writeUint32(h, base.templateVersion)
	writeLengthPrefixed(h, []byte(base.deployer))
	writeUint64(h, base.deployNonce)
	writeLengthPrefixed(h, base.contractContent)
	writeUint64(h, uint64(base.currentBlock))
	writeUint64(h, base.invokeCount)
	payload := struct {
		Version    int          `json:"version"`
		NextID     int64        `json:"nextId"`
		Invokes    uint64       `json:"invokes"`
		Items      []InvokeItem `json:"items,omitempty"`
		LimitOrder interface{}  `json:"limitOrder,omitempty"`
		AMM        interface{}  `json:"amm,omitempty"`
		Exchange   interface{}  `json:"exchange,omitempty"`
		Autopay    interface{}  `json:"autopay,omitempty"`
	}{Version: 1, NextID: state.NextItemID, Invokes: state.InvokeCount}
	if len(state.Items) > 0 {
		payload.Items = make([]InvokeItem, 0, len(state.Items))
		for _, item := range state.Items {
			if !item.Finished() {
				payload.Items = append(payload.Items, item)
			}
		}
	}
	switch runtime.contract.(type) {
	case *LimitOrderContract:
		payload.LimitOrder = state.LimitOrder
	case *AMMContract:
		payload.AMM = state.AMM
	case *ExchangeContract:
		payload.Exchange = state.Exchange
	case *AutopayContract:
		payload.Autopay = state.Autopay
	}
	raw, _ := json.Marshal(payload)
	writeLengthPrefixed(h, []byte(runtimeStateKey))
	writeLengthPrefixed(h, raw)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Check that the streamed writer hashes exactly its buffered binary bytes.
func bufferedTemplateBinaryRoot(runtime *ContractRuntime, state TemplateRuntimeState) [32]byte {
	h := sha256.New()
	base := runtime.base
	writeLengthPrefixed(h, []byte(base.address.EncodeAddress()))
	writeLengthPrefixed(h, []byte(base.templateName))
	writeUint32(h, base.templateVersion)
	writeLengthPrefixed(h, []byte(base.deployer))
	writeUint64(h, base.deployNonce)
	writeUint32(h, uint32(base.flags))
	writeLengthPrefixed(h, base.contractContent)
	managed := contractcommon.NewStateEncoder("")
	contractcommon.WriteManagedBalance(managed, base.managed)
	managedBytes, err := managed.Data()
	if err != nil {
		return [32]byte{}
	}
	h.Write(managedBytes)
	writeUint64(h, uint64(base.currentBlock))
	writeUint64(h, base.invokeCount)
	writeLengthPrefixed(h, []byte(runtimeStateKey))
	e := contractcommon.NewStateEncoder("")
	writeTemplateRootState(e, runtime.contract, state)
	data, err := e.Data()
	if err != nil {
		return [32]byte{}
	}
	h.Write(data)
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

func TestCompactTemplateRootManagedBalanceAndFlags(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("fallback_%t", fallback), func(t *testing.T) {
			runtime, _ := compactAutopayFixture(t, 2, 0)
			runtime.base.managed = contractcommon.ManagedBalance{Value: 20, Assets: append(testAsset("ordx:f:test", 100), testAsset("ordx:f:other", 50)...)}
			if fallback {
				runtime.SetState(runtimeStateKey, []byte("invalid state"))
				_, err := runtime.loadRuntimeState()
				require.Error(t, err, "exercise the raw-state fallback")
			}
			store := runtimeStoreWith(runtime)
			root, storeRoot := runtime.StateRoot(), store.StateRoot()
			require.NotEqual(t, [32]byte{}, root)
			combined := contractcommon.CombineStateRoots(storeRoot, [32]byte{}, [32]byte{})
			cases := []struct {
				name   string
				change func(*RuntimeBase)
			}{
				{"flags", func(base *RuntimeBase) { base.flags = contractcommon.ContractFlagNonClosable }},
				{"sats", func(base *RuntimeBase) { base.managed.Value++ }},
				{"asset_amount", func(base *RuntimeBase) { base.managed.Assets[0].Amount = *scommon.NewDefaultDecimal(101) }},
				{"asset_binding", func(base *RuntimeBase) { base.managed.Assets[0].BindingSat++ }},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					changedStore := store.Clone()
					changed, ok := changedStore.Get(runtime.Address())
					require.True(t, ok)
					tc.change(changed.base)
					require.Equal(t, runtime.base.state, changed.base.state, "only deployment policy or backing changes")
					require.NotEqual(t, root, changed.StateRoot())
					require.NotEqual(t, storeRoot, changedStore.StateRoot())
					require.NotEqual(t, combined, contractcommon.CombineStateRoots(changedStore.StateRoot(), [32]byte{}, [32]byte{}))
				})
			}
			before := runtime.base.managed.Clone()
			require.Equal(t, root, runtime.StateRoot())
			require.Equal(t, before, runtime.base.managed, "hashing must not normalize the live balance")
			runtime.base.managed.Assets[0], runtime.base.managed.Assets[1] = runtime.base.managed.Assets[1], runtime.base.managed.Assets[0]
			require.Equal(t, root, runtime.StateRoot(), "asset insertion order must not change the root")
		})
	}
}

func TestCompactTemplateStateRoundTripAndRoot(t *testing.T) {
	for _, contract := range []Contract{NewLimitOrderContract("ordx:f:test"), NewAMMContract("ordx:f:test", "100", 20, "2000"), NewExchangeContract("ordx:f:test", "ordx:f:other", ExchangePriceModeHeight, []ExchangePriceStep{{Threshold: "0", BPerA: "2"}}), NewAutopayContract("service", "recipient", "ordx:f:test", "1")} {
		t.Run(contract.TemplateName(), func(t *testing.T) {
			content, err := contract.Encode()
			require.NoError(t, err)
			address, _, err := DeriveContractAddress(TestnetContractPrefix, content, "deployer", 7)
			require.NoError(t, err)
			runtime, err := NewRuntimeWithDeployer(address, DeployPayload{SubType: contract.TemplateName(), Version: contract.Version(), DeployNonce: 7, Flags: contractcommon.ContractFlagNonClosable, ContractContent: content}, nil, "deployer")
			require.NoError(t, err)
			require.NoError(t, runtime.base.managed.Credit(20, testAsset("ordx:f:test", 100)))
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			state.Items = []InvokeItem{{ID: 0, Action: InvokeAPIClose, OrderType: OrderTypeClose, GasFee: scommon.NewDecimalWithScale(125, 3), InAmt: scommon.NewDecimal(0, 8)}}
			require.NoError(t, runtime.saveRuntimeState(state))
			originalJSON, err := json.Marshal(state)
			require.NoError(t, err)
			var normalized TemplateRuntimeState
			require.NoError(t, normalized.UnmarshalJSON(originalJSON))
			restored, err := runtime.RuntimeState()
			require.NoError(t, err)
			require.Equal(t, normalized, restored)
			require.Equal(t, bufferedTemplateBinaryRoot(runtime, normalized), runtime.StateRoot())
			store := runtimeStoreWith(runtime)
			raw, err := store.MarshalBinary()
			require.NoError(t, err)
			require.False(t, json.Valid(raw))
			decoded, err := DecodeRuntimeStore(raw, nil)
			require.NoError(t, err)
			require.Equal(t, store.StateRoot(), decoded.StateRoot())
			decodedRuntime, ok := decoded.Get(address)
			require.True(t, ok)
			require.Equal(t, runtime.DeploymentFlags(), decodedRuntime.DeploymentFlags())
			require.Equal(t, runtime.base.managed, decodedRuntime.base.managed)
			inner, _ := runtime.GetState(runtimeStateKey)
			require.False(t, json.Valid(inner))
			for _, bad := range [][]byte{[]byte(`{"runtimes":[]}`), raw[:len(raw)-1], append(bytes.Clone(raw), 0)} {
				_, err = DecodeRuntimeStore(bad, nil)
				require.Error(t, err)
			}
		})
	}
}
func TestCompactTemplateMapOrderAndDecimalBoundaries(t *testing.T) {
	a, b := TemplateRuntimeState{}, TemplateRuntimeState{}
	a.AutopayData().AutopayDelegates = map[string]AutopayDelegate{"zeta": {Balance: scommon.NewDefaultDecimal(2)}, "alpha": {Balance: scommon.NewDefaultDecimal(1)}}
	b.AutopayData().AutopayDelegates = map[string]AutopayDelegate{"alpha": {Balance: scommon.NewDefaultDecimal(1)}, "zeta": {Balance: scommon.NewDefaultDecimal(2)}}
	raw, err := encodeTemplateRuntimeState(a)
	require.NoError(t, err)
	other, err := encodeTemplateRuntimeState(b)
	require.NoError(t, err)
	require.Equal(t, raw, other)
	bad := TemplateRuntimeState{Items: []InvokeItem{{ID: 0, Action: InvokeAPIClose, OrderType: OrderTypeClose, InAmt: &scommon.Decimal{Precision: 0, Value: scommon.NewDefaultDecimal(1).Value}}}}
	bad.Items[0].InAmt.Value.SetString(strings.Repeat("1", 129), 10)
	_, err = encodeTemplateRuntimeState(bad)
	require.Error(t, err)
	bad.Items[0].InAmt = scommon.NewDecimalWithScale(1, 11)
	_, err = encodeTemplateRuntimeState(bad)
	require.Error(t, err)
}

// The previous persistence path is retained only in this comparison fixture.
// Its inner state remains JSON, so use the previous reader explicitly rather
// than feeding it through the new binary reader or adding conversion overhead.
func marshalTemplateJSONBaseline(store *RuntimeStore) ([]byte, error) {
	snapshot := runtimeStoreSnapshot{}
	for _, key := range store.sortedKeys() {
		runtime := store.runtimes[key]
		if err := runtime.base.flags.Validate(); err != nil {
			return nil, err
		}
		snapshot.Runtimes = append(snapshot.Runtimes, snapshotRuntime(runtime))
	}
	return json.Marshal(snapshot)
}

func decodeTemplateJSONBaseline(data []byte) (*RuntimeStore, error) {
	var snapshot runtimeStoreSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	store := NewRuntimeStore()
	for _, item := range snapshot.Runtimes {
		if item.ManagedBalance == nil {
			return nil, fmt.Errorf("missing managed balance")
		}
		if err := item.ManagedBalance.Validate(); err != nil {
			return nil, err
		}
		address, err := contractcommon.DecodeContractAddress(item.Address)
		if err != nil {
			return nil, err
		}
		runtime, err := NewRuntimeWithDeployer(address, DeployPayload{Type: ContractTypeTemplate, SubType: item.TemplateName, Version: item.TemplateVersion, GasLimit: 1, DeployNonce: item.DeployNonce, Flags: item.Flags, ContractContent: item.ContractContent}, nil, item.Deployer)
		if err != nil {
			return nil, err
		}
		if store.Exists(address) {
			return nil, fmt.Errorf("duplicate runtime")
		}
		runtime.base.currentBlock, runtime.base.invokeCount = item.CurrentBlock, item.InvokeCount
		runtime.base.managed = item.ManagedBalance.Clone()
		runtime.base.state = make(map[string][]byte, len(item.State))
		for key, value := range item.State {
			runtime.base.state[key] = bytes.Clone(value)
		}
		if data := runtime.base.state[runtimeStateKey]; len(data) != 0 {
			var state TemplateRuntimeState
			if err := state.UnmarshalJSON(data); err != nil {
				return nil, err
			}
		}
		store.Add(runtime)
	}
	return store, nil
}

func TestCompactTemplateStatePerformance(t *testing.T) {
	for _, count := range []int{20, 500, 1001} {
		for _, pending := range []int{0, 20} {
			t.Run(fmt.Sprintf("delegates_%d_pending_%d", count, pending), func(t *testing.T) {
				runtime, state := compactAutopayFixture(t, count, pending)
				raw, err := encodeTemplateRuntimeState(state)
				require.NoError(t, err)
				legacy, err := json.Marshal(state)
				require.NoError(t, err)
				var jsonState TemplateRuntimeState
				require.NoError(t, jsonState.UnmarshalJSON(legacy))
				binaryState, err := decodeTemplateRuntimeState(raw)
				require.NoError(t, err)
				require.Equal(t, jsonState, binaryState)
				require.Equal(t, bufferedTemplateBinaryRoot(runtime, jsonState), runtime.StateRoot())
				store := runtimeStoreWith(runtime)
				storeBytes, err := store.MarshalBinary()
				require.NoError(t, err)
				legacyStore := store.Clone()
				legacyRuntime, ok := legacyStore.Get(runtime.Address())
				require.True(t, ok)
				legacyRuntime.base.state[runtimeStateKey] = bytes.Clone(legacy)
				legacyStoreBytes, err := marshalTemplateJSONBaseline(legacyStore)
				require.NoError(t, err)
				restoredStore, err := DecodeRuntimeStore(storeBytes, nil)
				require.NoError(t, err)
				require.Equal(t, store.StateRoot(), restoredStore.StateRoot())
				oldStore, err := decodeTemplateJSONBaseline(legacyStoreBytes)
				require.NoError(t, err)
				oldRuntime, ok := oldStore.Get(runtime.Address())
				require.True(t, ok)
				var oldState TemplateRuntimeState
				require.NoError(t, oldState.UnmarshalJSON(oldRuntime.base.state[runtimeStateKey]))
				require.Equal(t, binaryState, oldState)
				require.Equal(t, runtime.StateRoot(), bufferedTemplateBinaryRoot(oldRuntime, oldState))
				codecs := []struct {
					scope  string
					name   string
					encode func() ([]byte, error)
					decode func() error
					size   int
				}{
					{"state", "json", func() ([]byte, error) { return json.Marshal(state) }, func() error { var out TemplateRuntimeState; return out.UnmarshalJSON(legacy) }, len(legacy)},
					{"state", "compact", func() ([]byte, error) { return encodeTemplateRuntimeState(state) }, func() error { _, err := decodeTemplateRuntimeState(raw); return err }, len(raw)},
					{"store", "json", func() ([]byte, error) { return marshalTemplateJSONBaseline(legacyStore) }, func() error { _, err := decodeTemplateJSONBaseline(legacyStoreBytes); return err }, len(legacyStoreBytes)},
					{"store", "compact", store.MarshalBinary, func() error { _, err := DecodeRuntimeStore(storeBytes, nil); return err }, len(storeBytes)},
				}
				for _, codec := range codecs {
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
						t.Logf("METRIC delegates=%d pending=%d scope=%s codec=%s op=%s bytes=%d ns_op=%d B_op=%d allocs_op=%d", count, pending, codec.scope, codec.name, op, codec.size, result.NsPerOp(), result.AllocedBytesPerOp(), result.AllocsPerOp())
					}
				}
			})
		}
	}
}

func compactAutopayFixture(t *testing.T, count, pending int) (*ContractRuntime, TemplateRuntimeState) {
	t.Helper()
	runtime := testAutopayRuntime(t, "recipient", DefaultGasConfig().GasAssetName, "2000")
	for i := 0; i < pending; i++ {
		_, err := runtime.ApplyDefaultInvoke(ApplyInvokeRequest{Invoker: fmt.Sprintf("delegate-%04d", i), Height: 100, FundingOutput: testContractOutput(fmt.Sprintf("funding-%04d", i), 0, runtime.Address(), 0, testAsset(DefaultGasConfig().GasAssetName, 1000))})
		require.NoError(t, err)
	}
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	if state.AutopayData().AutopayDelegates == nil {
		state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate)
	}
	for i := pending; i < count; i++ {
		state.AutopayData().AutopayDelegates[fmt.Sprintf("delegate-%04d", i)] = AutopayDelegate{Balance: scommon.NewDefaultDecimal(1000), TotalPaid: scommon.NewDefaultDecimal(0)}
	}
	require.NoError(t, runtime.saveRuntimeState(state))

	return runtime, state
}

func TestCompactTemplateRootProjectionAndSensitivity(t *testing.T) {
	runtime, _ := compactAutopayRootFixture(t, 20, 2)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	raw, ok := runtime.GetState(runtimeStateKey)
	require.True(t, ok)
	root := runtime.StateRoot()
	require.NotEqual(t, [32]byte{}, root)
	for i := 0; i < 3; i++ {
		require.Equal(t, root, runtime.StateRoot())
	}
	unchanged, _ := runtime.GetState(runtimeStateKey)
	require.Equal(t, raw, unchanged, "hashing must not mutate persisted items")
	require.Equal(t, bufferedTemplateBinaryRoot(runtime, state), root)

	// Identical delegate records inserted in reverse order retain the root.
	delegates := state.Autopay.AutopayDelegates
	state.Autopay.AutopayDelegates = make(map[string]AutopayDelegate, len(delegates))
	keys := contractcommon.SortedStateKeys(delegates)
	for i := len(keys) - 1; i >= 0; i-- {
		state.Autopay.AutopayDelegates[keys[i]] = delegates[keys[i]]
	}
	require.NoError(t, runtime.saveRuntimeState(state))
	require.Equal(t, root, runtime.StateRoot())

	// Unused branches and finished records remain outside this template's root.
	state.AMM = &AMMRunningData{}
	finished := state.Items[0]
	finished.ID = 100
	finished.Done = ItemStatusInit + 1
	finished.StatsApplied = true
	state.Items = append(state.Items, finished)
	require.NoError(t, runtime.saveRuntimeState(state))
	require.Equal(t, root, runtime.StateRoot())
	state.Items[len(state.Items)-1].CallID = "finished record changed"
	require.NoError(t, runtime.saveRuntimeState(state))
	require.Equal(t, root, runtime.StateRoot())

	state.Items[0].CallID += " changed"
	require.NoError(t, runtime.saveRuntimeState(state))
	pendingChanged := runtime.StateRoot()
	require.NotEqual(t, root, pendingChanged)
	state.Items[0], state.Items[1] = state.Items[1], state.Items[0]
	require.NoError(t, runtime.saveRuntimeState(state))
	reordered := runtime.StateRoot()
	require.NotEqual(t, pendingChanged, reordered, "pending order is significant")
	state.Autopay.NextPayHeight++
	require.NoError(t, runtime.saveRuntimeState(state))
	activeChanged := runtime.StateRoot()
	require.NotEqual(t, reordered, activeChanged)
	state.NextItemID++
	require.NoError(t, runtime.saveRuntimeState(state))
	require.NotEqual(t, activeChanged, runtime.StateRoot())
}

func TestCompactTemplateRootPerformance(t *testing.T) {
	for _, count := range []int{20, 500, 1001} {
		for _, pending := range []int{0, 20} {
			t.Run(fmt.Sprintf("delegates_%d_pending_%d", count, pending), func(t *testing.T) {
				runtime, _ := compactAutopayRootFixture(t, count, pending)
				state, err := runtime.RuntimeState()
				require.NoError(t, err)
				root := runtime.StateRoot()
				require.Equal(t, bufferedTemplateBinaryRoot(runtime, state), root)
				require.NotEqual(t, legacyTemplateJSONRoot(runtime, state), root)
				for _, codec := range []string{"json", "compact"} {
					result := testing.Benchmark(func(b *testing.B) {
						b.ReportAllocs()
						for i := 0; i < b.N; i++ {
							var got [32]byte
							if codec == "json" {
								// Both paths include the same compact load/validation cost.
								loaded, err := runtime.loadRuntimeState()
								if err != nil {
									b.Fatal(err)
								}
								got = legacyTemplateJSONRoot(runtime, loaded)
							} else {
								got = runtime.StateRoot()
							}
							if got == ([32]byte{}) {
								b.Fatal("empty root")
							}
						}
					})
					t.Logf("METRIC_ROOT delegates=%d pending=%d codec=%s ns_op=%d B_op=%d allocs_op=%d", count, pending, codec, result.NsPerOp(), result.AllocedBytesPerOp(), result.AllocsPerOp())
				}
			})
		}
	}
}

// Default funding is immediately dealt in Autopay; create actual pending
// cancellations through ApplyInvoke rather than treating funding history as pending.
func compactAutopayRootFixture(t *testing.T, count, pending int) (*ContractRuntime, TemplateRuntimeState) {
	t.Helper()
	runtime, _ := compactAutopayFixture(t, count, 0)
	param, err := (&CloseInvokeParam{}).Encode()
	require.NoError(t, err)
	for i := 0; i < pending; i++ {
		item, err := runtime.ApplyInvoke(ApplyInvokeRequest{Action: InvokeAPICancel, Param: param, Invoker: fmt.Sprintf("delegate-%04d", i), CallID: fmt.Sprintf("cancel-%04d", i), Height: 100, FundingOutput: testContractOutput(fmt.Sprintf("cancel-funding-%04d", i), 0, runtime.Address(), 0, nil)})
		require.NoError(t, err)
		require.NotNil(t, item)
		require.False(t, item.Finished(), "fixture must contain pending items")
	}
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.Items, pending)
	for _, item := range state.Items {
		require.False(t, item.Finished())
	}
	return runtime, state
}
