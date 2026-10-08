package main

import (
	"encoding/json"
	"strings"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/indexer/indexer"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/indexer/indexer/evmsource"
)

func nodeDKVSConfig(db database.DB, l1IndexerBaseURL string) *indexer.DKVSIntegrationConfig {
	sourceVerifier := evmsource.Verifier{Params: activeNetParams.Params, GasConfig: node.DefaultGasConfig()}
	if cfg != nil {
		sourceVerifier.CompilerPath = cfg.EVMSourceSolc
		if gas, err := configuredContractGasConfig(); err == nil {
			sourceVerifier.GasConfig = gas
		}
	}
	return &indexer.DKVSIntegrationConfig{
		ResolverL1NSBaseURL:  l1IndexerBaseURL,
		AutopayStateProvider: localAutopayStateProvider{db: db},
		EVMSourceVerifier:    sourceVerifier.Verify,
	}
}

type localAutopayStateProvider struct{ db database.DB }

// Index callbacks can run with chainLock held. Read only committed template
// state, without acquiring chainLock or waiting on the local RPC request queue.
// AUTOPAY's view uses the runtime's persisted CurrentBlock/payment history.
func (p localAutopayStateProvider) GetAutopayState(address string) (*dkvs.AutopayContractState, error) {
	address = strings.TrimSpace(address)
	addr, err := contractcommon.DecodeContractAddress(address)
	if err != nil {
		return nil, err
	}
	_, store, err := node.NewTemplateStateStore(p.db).LoadTip()
	if err != nil {
		return nil, err
	}
	runtime, ok := store.Get(addr)
	if !ok {
		return nil, dkvs.ErrInvalidFeeProof
	}
	if _, ok := runtime.Contract().(*template.AutopayContract); !ok {
		return nil, dkvs.ErrInvalidFeeProof
	}
	view, err := runtime.StateView(framework.StateViewContext{ContractAddress: address})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	return dkvs.DecodeAutopayContractState(raw, address)
}
