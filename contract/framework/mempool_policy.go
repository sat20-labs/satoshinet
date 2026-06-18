package framework

import (
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

type MempoolPolicy interface {
	CheckInputStandard(tx *btcutil.Tx, view any, params *chaincfg.Params) error
	CheckOutputStandard(tx *wire.MsgTx, params *chaincfg.Params) error
	ClassifyTx(tx *wire.MsgTx, params *chaincfg.Params) (TxClass, bool, error)
}
