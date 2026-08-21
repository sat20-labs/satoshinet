package node

import (
	"testing"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
)

func candidateChildBlock(parent chainhash.Hash) *btcutil.Block {
	block := btcutil.NewBlock(&wire.MsgBlock{
		Header: wire.BlockHeader{PrevBlock: parent},
	})
	block.SetHeight(2)
	return block
}

func TestCandidateBranchRuntimeUsesTransientParentPostState(t *testing.T) {
	parentHash := chainhash.Hash{0xa5, 0x11}
	child := candidateChildBlock(parentHash)

	t.Run("template", func(t *testing.T) {
		transient := template.NewRuntimeStore()
		transient.Add(testNodeAutopayRuntime(t, "candidate-recipient", "ordx:f:test", "10"))
		wantRoot := transient.StateRoot()

		factoryCalls := 0
		validator := NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{
			NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*template.RuntimeStore, error) {
				factoryCalls++
				return template.NewRuntimeStore(), nil
			},
		})
		validator.rememberPostState(&parentHash, transient)

		got, err := validator.runtime(child, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.StateRoot() != wantRoot {
			t.Fatalf("template runtime root=%x, want transient parent %x", got.StateRoot(), wantRoot)
		}
		if factoryCalls != 0 {
			t.Fatalf("template parent post-state fell through to persistent factory: calls=%d", factoryCalls)
		}
	})

	t.Run("agent", func(t *testing.T) {
		transient := testReadyAgentRuntimeStore(t, agent.TimeBaseHeight, 10, 30)
		wantRoot := transient.StateRoot()

		factoryCalls := 0
		validator := NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{
			NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*agent.RuntimeStore, error) {
				factoryCalls++
				return agent.NewRuntimeStore(), nil
			},
		})
		validator.rememberPostState(&parentHash, transient)

		got, err := validator.runtime(child, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.StateRoot() != wantRoot {
			t.Fatalf("agent runtime root=%x, want transient parent %x", got.StateRoot(), wantRoot)
		}
		if factoryCalls != 0 {
			t.Fatalf("agent parent post-state fell through to persistent factory: calls=%d", factoryCalls)
		}
	})

	t.Run("evm", func(t *testing.T) {
		transientRuntime := evm.NewRuntime(nil)
		contract := testContractAddressForBlockchain(t)
		transientRuntime.SetCode(evm.ContractAddressHash(contract), []byte{0x00})
		wantRoot := transientRuntime.State.StateRoot()

		factoryCalls := 0
		validator := NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{
			NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*evm.Runtime, error) {
				factoryCalls++
				return evm.NewRuntime(nil), nil
			},
		})
		validator.rememberPostState(&parentHash, transientRuntime.State)

		got, err := validator.runtime(child, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.State == nil {
			t.Fatal("missing EVM runtime state")
		}
		if got.State.StateRoot() != wantRoot {
			t.Fatalf("EVM runtime root=%x, want transient parent %x", got.State.StateRoot(), wantRoot)
		}
		if factoryCalls != 1 {
			t.Fatalf("EVM runtime must preserve factory configuration before state override: calls=%d", factoryCalls)
		}
	})
}
