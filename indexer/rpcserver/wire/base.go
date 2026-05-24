package wire

import (
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
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

type ContractContentResp struct {
	indexerwire.BaseResp
	Contracts []string `json:"contracts"`
}

type DeployedContractResp struct {
	indexerwire.BaseResp
	ContractURLs []string `json:"url"`
}

type ContractStatusResp struct {
	indexerwire.BaseResp
	Status string `json:"status"`
}

type TemplateContractsResp struct {
	indexerwire.BaseResp
	Total int                          `json:"total"`
	Data  []*tmplcontract.ContractInfo `json:"data"`
}

type TemplateContractResp struct {
	indexerwire.BaseResp
	Data *tmplcontract.ContractInfo `json:"data"`
}

type TemplateContractHistoryResp struct {
	indexerwire.BaseResp
	Total int                          `json:"total"`
	Data  []tmplcontract.HistoryRecord `json:"data"`
}

type TemplateContractAnalytics struct {
	Address        string                   `json:"address"`
	TemplateName   string                   `json:"templateName"`
	Version        uint32                   `json:"version"`
	UpdatedHeight  int64                    `json:"updatedHeight"`
	Running        tmplcontract.RunningData `json:"running"`
	TotalItems     int                      `json:"totalItems"`
	ActiveItems    int                      `json:"activeItems"`
	FinishedItems  int                      `json:"finishedItems"`
	StatusCount    map[int]int              `json:"statusCount"`
	OrderTypeCount map[int]int              `json:"orderTypeCount"`
}

type TemplateContractUserStatus struct {
	Address       string                    `json:"address"`
	Contract      string                    `json:"contract"`
	TotalItems    int                       `json:"totalItems"`
	ActiveItems   int                       `json:"activeItems"`
	FinishedItems int                       `json:"finishedItems"`
	Items         []tmplcontract.InvokeItem `json:"items"`
}
