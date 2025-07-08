package wire

import (
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

type BlockInfoData struct {
	indexerwire.BaseResp
	Data *common.BlockInfo `json:"info"`
}

type AscendResp struct {
	indexerwire.BaseResp
	Data *common.AscendData `json:"data"`
}

type DescendResp struct {
	indexerwire.BaseResp
	Data *common.DescendData `json:"data"`
}

type AllCoreNodeResp struct {
	indexerwire.BaseResp
	Data []string `json:"data"`
}

type CheckCoreNodeResp struct {
	indexerwire.BaseResp
	Data bool `json:"data"`
}

type GetCoreNodeInfoResp struct {
	indexerwire.BaseResp
	IsCoreNdoe bool     `json:"data"`
	Childs     []string `json:"childs"`
}
