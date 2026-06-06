package wire

import (
	"encoding/json"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
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

type ChannelLedgerResp struct {
	indexerwire.BaseResp
	Data []*common.ChannelLedgerEntry `json:"data"`
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
	Total int             `json:"total"`
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
	Data *common.CoreNodeInfo `json:"data"`
}

type MinerInfo struct {
	*common.MinerInfo
	IsCoreNode bool `json:"isCoreNode"`
	ChildCount int  `json:"childCount"`
}

type GetMinerInfoResp struct {
	indexerwire.BaseResp
	Data *MinerInfo `json:"data"`
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

type ContractResp struct {
	indexerwire.BaseResp
	Status string          `json:"status,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

type ContractListResp struct {
	indexerwire.BaseResp
	Total        int                              `json:"total"`
	Contracts    []string                         `json:"contracts,omitempty"`
	ContractURLs []string                         `json:"url,omitempty"`
	Data         []contractcommon.ContractSummary `json:"data"`
}

type ContractHistoryResp struct {
	indexerwire.BaseResp
	Total  int                                    `json:"total"`
	Status string                                 `json:"status,omitempty"`
	Data   []contractcommon.ContractHistoryRecord `json:"data,omitempty"`
}
