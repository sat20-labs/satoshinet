package framework

import "github.com/sat20-labs/satoshinet/wire"

type ResultBuildRequest struct {
	Txs    []*wire.MsgTx
	Prefix string
}

type ResultBuildResult struct {
	ResultTxs []*wire.MsgTx
	StateRoot [32]byte
}

type BlockResultModule interface {
	BuildBlockResults(req ResultBuildRequest) (ResultBuildResult, ExecutionResult, error)
}
