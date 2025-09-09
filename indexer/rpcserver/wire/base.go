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

type ReferrerResp struct {
	indexerwire.BaseResp
	Data *common.ReferrerInfo `json:"referrer"`
}

type ReferreeInfo struct {
	Name      string `json:"name"`
	BindBlock int    `json:"bindBlock"`
}

type ReferreeResp struct {
	indexerwire.BaseResp
	Total int   `json:"total"`
	Data  []*ReferreeInfo `json:"referrees"`
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
	Data *common.CoreNodeInfo     `json:"data"`
}

type GetMinerInfoResp struct {
	indexerwire.BaseResp
	Data *common.MinerInfo     `json:"data"`
}

type TickersResp struct {
	indexerwire.BaseResp
	Total int                  `json:"total"`
	Data  []*common.TickerInfo `json:"data"`
}

type TickerInfoResp struct {
	indexerwire.BaseResp
	Data *common.TickerInfo `json:"data"`
}
