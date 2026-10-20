package template

import (
	"encoding/json"
	"testing"

	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func TestLimitOrderStateViewIncludesClosed(t *testing.T) {
	runtime := newLimitOrderClosedViewTestRuntime(t)
	state := TemplateRuntimeState{}
	state.LimitOrderData().Closed = true
	if err := runtime.saveRuntimeState(state); err != nil {
		t.Fatalf("save runtime state: %v", err)
	}

	view, err := runtime.StateView(contractframework.StateViewContext{})
	if err != nil {
		t.Fatalf("build state view: %v", err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal state view: %v", err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal state view JSON: %v", err)
	}
	closed, ok := fields["closed"]
	if !ok || closed != true {
		t.Fatalf("state view should expose closed:true; JSON=%s; closed=%v present=%t", encoded, closed, ok)
	}
}

// Keep the runtime construction identical to the template package's existing newLimitOrderClosedViewTestRuntime helper.
func newLimitOrderClosedViewTestRuntime(t *testing.T) *ContractRuntime {
	t.Helper()
	contract := NewLimitOrderContract("ordx:f:test")
	content, err := contract.Encode()
	if err != nil {
		t.Fatalf("encode limit order contract: %v", err)
	}
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateLimitOrder,
		Version:         CurrentTemplateVersion,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, content, "deployer-address", deploy.DeployNonce)
	if err != nil {
		t.Fatalf("derive contract address: %v", err)
	}
	runtime, err := NewRuntimeWithDeployer(addr, deploy, nil, "deployer-address")
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	return runtime
}

func TestLimitOrderStateViewOpenControl(t *testing.T) {
	runtime := newLimitOrderClosedViewTestRuntime(t)
	view, err := runtime.StateView(contractframework.StateViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	limit, ok := view.(LimitOrderStateView)
	if !ok || limit.Closed {
		t.Fatalf("open contract must stay open: %#v", view)
	}
}
