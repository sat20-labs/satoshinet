package evm

import (
	"bytes"
	"testing"
)

func TestRuntimeCallSimpleReturn(t *testing.T) {
	contract := ContractAddressHash(testContract(t))
	caller, err := ParseEVMAddressHex("11112233445566778899aabbccddeeff00112233")
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(nil)
	// Runtime bytecode:
	// PUSH1 0x2a PUSH1 0x00 MSTORE PUSH1 0x20 PUSH1 0x00 RETURN
	rt.SetCode(contract, []byte{0x60, 0x2a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3})
	res := rt.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: contract.String(),
		Gas:           100000,
		Block:         BlockContext{Number: 1, Time: 1, GasLimit: 1000000, FixedGasPrice: 1},
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Status != ResultStatusSuccess {
		t.Fatalf("status got %d want %d", res.Status, ResultStatusSuccess)
	}
	want := append(bytes.Repeat([]byte{0}, 31), 0x2a)
	if !bytes.Equal(res.ReturnData, want) {
		t.Fatalf("return data got %x want %x", res.ReturnData, want)
	}
	if res.GasUsed == 0 {
		t.Fatal("expected gas to be consumed")
	}
}

func TestRuntimeDeployThenCall(t *testing.T) {
	caller, err := ParseEVMAddressHex("11112233445566778899aabbccddeeff00112233")
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(nil)

	deploy := rt.Deploy(DeployRequest{
		CallerAddress: caller.String(),
		CallID:        "deploy-1",
		InitCode:      return42InitCode(),
		Gas:           200000,
		DeployNonce:   3,
		Block:         BlockContext{Number: 1, Time: 1, GasLimit: 1000000, FixedGasPrice: 1},
	})
	if deploy.Err != nil {
		t.Fatal(deploy.Err)
	}
	if deploy.Status != ResultStatusSuccess {
		t.Fatalf("deploy status got %d want %d", deploy.Status, ResultStatusSuccess)
	}
	if len(deploy.RuntimeCode) == 0 {
		t.Fatal("expected deployed runtime code")
	}

	call := rt.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: deploy.Contract.MustEncode(),
		CallID:        "call-1",
		Gas:           100000,
		Block:         BlockContext{Number: 1, Time: 1, GasLimit: 1000000, FixedGasPrice: 1},
	})
	if call.Err != nil {
		t.Fatal(call.Err)
	}
	want := append(bytes.Repeat([]byte{0}, 31), 0x2a)
	if !bytes.Equal(call.ReturnData, want) {
		t.Fatalf("return data got %x want %x", call.ReturnData, want)
	}
}

func return42InitCode() []byte {
	runtime := []byte{0x60, 0x2a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}
	init := []byte{
		0x60, byte(len(runtime)),
		0x60, 0x0c,
		0x60, 0x00,
		0x39,
		0x60, byte(len(runtime)),
		0x60, 0x00,
		0xf3,
	}
	return append(init, runtime...)
}
