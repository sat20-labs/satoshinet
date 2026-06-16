package indexer

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/indexer/common"
	localwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
)

const QueryParamDefaultLimit = "100"

type Handle struct {
	model *Model
}

func NewHandle(indexer shareIndexer.Indexer) *Handle {
	return &Handle{
		model: NewModel(indexer),
	}
}

// @Summary Health Check
// @Description Check the health status of the service
// @Tags ordx
// @Produce json
// @Success 200 {object} HealthStatusResp "Successful response"
// @Router /health [get]
func (s *Handle) getHealth(c *gin.Context) {
	rsp := &indexerwire.HealthStatusResp{
		Status:    "ok",
		Version:   common.SATOSHINET_INDEXER_VERSION,
		BaseDBVer: s.model.indexer.GetBaseDBVer(),
	}

	tip := s.model.indexer.GetChainTip()
	sync := s.model.indexer.GetSyncHeight()
	code := 200
	if tip != sync && tip != sync+1 {
		code = 201
		rsp.Status = "syncing"
	}

	c.JSON(code, rsp)
}

func (s *Handle) getTickerList(c *gin.Context) {
	resp := &localwire.TickersResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	protocol := c.Param("protocol")
	start, err := strconv.Atoi(c.DefaultQuery("start", "0"))
	if err != nil {
		start = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", QueryParamDefaultLimit))
	if err != nil {
		limit = 100
	}
	resp.Data, resp.Total = s.model.GetTickerList(protocol, start, limit)

	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getTickerInfo(c *gin.Context) {
	resp := &localwire.TickerInfoResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	tickerName := c.Param("ticker")
	tickerInfo, err := s.model.GetTickerInfo(tickerName)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = tickerInfo
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getHolderListV3(c *gin.Context) {
	resp := &indexerwire.HolderListRespV3{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	tickerName := c.Param("ticker")
	start, err := strconv.Atoi(c.DefaultQuery("start", "0"))
	if err != nil {
		start = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", QueryParamDefaultLimit))
	if err != nil {
		limit = 100
	}
	holderlist, total, err := s.model.GetHolderListV3(tickerName, uint64(start), uint64(limit))
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = &indexerwire.HolderListDataV3{
		ListResp: indexerwire.ListResp{
			Total: total,
			Start: int64(start),
		},
		Detail: holderlist,
	}
	c.JSON(http.StatusOK, resp)
}

// @Summary Retrieves available UTXOs
// @Description Get UTXOs in a address and its value is greater than the specific value. If value=0, get all UTXOs
// @Tags ordx
// @Produce json
// @Param address path string true "address"
// @Param value path int64 true "value"
// @Security Bearer
// @Success 200 {array} PlainUtxo "Successful response"
// @Failure 401 "Invalid API Key"
// @Router /utxo/address/{address}/{value} [post]
func (s *Handle) getPlainUtxos(c *gin.Context) {
	resp := &indexerwire.PlainUtxosResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Total: 0,
		Data:  nil,
	}

	value, err := strconv.ParseInt(c.Param("value"), 10, 64)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	address := c.Param("address")
	start, err := strconv.Atoi(c.DefaultQuery("start", "0"))
	if err != nil {
		start = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil {
		limit = 0
	}
	availableUtxoList, total, err := s.model.getPlainUtxos(address, value, start, limit)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Total = total
	resp.Data = availableUtxoList
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getAllUtxos(c *gin.Context) {
	resp := &indexerwire.AllUtxosResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Total:      0,
		PlainUtxos: nil,
		OtherUtxos: nil,
	}

	address := c.Param("address")
	start, err := strconv.Atoi(c.DefaultQuery("start", "0"))
	if err != nil {
		start = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil {
		limit = 0
	}
	PlainUtxos, OtherUtxos, total, err := s.model.getAllUtxos(address, start, limit)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Total = total
	resp.PlainUtxos = PlainUtxos
	resp.OtherUtxos = OtherUtxos
	c.JSON(http.StatusOK, resp)
}

// @Summary Get the current btc height
// @Description the current btc height
// @Tags ordx
// @Produce json
// @Security Bearer
// @Success 200 {object} BestHeightResp "Successful response"
// @Failure 401 "Invalid API Key"
// @Router /bestheight [get]
func (s *Handle) getBestHeight(c *gin.Context) {
	resp := &indexerwire.BestHeightResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: map[string]int{"height": s.model.GetSyncHeight()},
	}
	c.JSON(http.StatusOK, resp)
}

// @Summary Get the height block info
// @Description the height block info
// @Tags ordx
// @Produce json
// @Security Bearer
// @Success 200 {object} BestHeightResp "Successful response"
// @Failure 401 "Invalid API Key"
// @Router /height [get]
func (s *Handle) getBlockInfo(c *gin.Context) {
	resp := &localwire.BlockInfoData{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
	}

	height, err := strconv.ParseInt(c.Param("height"), 10, 32)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	result, err := s.model.GetBlockInfo(int(height))
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
	} else {
		resp.Data = result
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getExistingUtxos(c *gin.Context) {
	resp := &indexerwire.ExistingUtxoResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
	}

	var req indexerwire.UtxosReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	result, err := s.model.GetExistingUtxos(&req)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
	} else {
		resp.ExistingUtxos = result
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getAscendData(c *gin.Context) {
	resp := &localwire.AscendResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	utxo := c.Param("utxo")
	result, err := s.model.GetAscend(utxo)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	// TODO 因为Decimal json序列化的修改，暂时将资产列表清空，因为前端还没用到
	result.Assets = nil

	resp.Data = result
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDescendData(c *gin.Context) {
	resp := &localwire.DescendResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	utxo := c.Param("utxo")
	result, err := s.model.GetDescend(utxo)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = result
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getChannelLedger(c *gin.Context) {
	resp := &localwire.ChannelLedgerResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	channel := c.Param("channel")
	result, err := s.model.GetChannelLedger(channel)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = result
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getChannelStateEvents(c *gin.Context) {
	resp := &localwire.ChannelStateEventResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	channel := c.Param("channel")
	result, err := s.model.GetChannelStateEvents(channel)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = result
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) recordChannelStateEvent(c *gin.Context) {
	resp := &localwire.ChannelStateEventResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
	}

	var req localwire.ChannelStateEventReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	if !req.ConfirmUnsafeTestOnly {
		resp.Code = -1
		resp.Msg = "confirm_unsafe_test_only is required"
		c.JSON(http.StatusOK, resp)
		return
	}
	if req.Data == nil {
		resp.Code = -1
		resp.Msg = "data is required"
		c.JSON(http.StatusOK, resp)
		return
	}
	if err := s.model.RecordChannelStateEvent(req.Data); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = []*common.ChannelStateEvent{req.Data}
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getReferrer(c *gin.Context) {
	resp := &localwire.ReferrerResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	addr := c.Param("address")
	result, err := s.model.GetReferrer(addr)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = result
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getReferree(c *gin.Context) {
	resp := &localwire.ReferreeResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	name := c.Param("name")
	start, err := strconv.Atoi(c.DefaultQuery("start", "0"))
	if err != nil {
		start = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", QueryParamDefaultLimit))
	if err != nil {
		limit = 100
	}
	resp.Data, resp.Total = s.model.GetReferree(name, start, limit)

	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getAllCoreNode(c *gin.Context) {
	resp := &localwire.AllCoreNodeResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	result, err := s.model.GetAllCoreNode()
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = result
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) checkCoreNode(c *gin.Context) {
	resp := &localwire.CheckCoreNodeResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: false,
	}

	pubkey := c.Param("pubkey")
	resp.Data = s.model.CheckCoreNode(pubkey)

	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getCoreNodeInfo(c *gin.Context) {
	resp := &localwire.GetCoreNodeInfoResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
	}

	pubkey := c.Param("pubkey")
	resp.Data = s.model.GetCoreNodeInfo(pubkey)
	if resp.Data == nil {
		resp.Code = -1
		resp.Msg = "not found"
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Handle) checkMiner(c *gin.Context) {
	resp := &localwire.CheckCoreNodeResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: false,
	}

	pubkey := c.Param("pubkey")
	resp.Data = s.model.CheckMiner(pubkey)

	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getMinerInfo(c *gin.Context) {
	resp := &localwire.GetMinerInfoResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
	}

	pubkey := c.Param("pubkey")
	corenode := s.model.GetCoreNodeInfo(pubkey)
	if corenode != nil {
		resp.Data = &localwire.MinerInfo{
			MinerInfo:  &corenode.MinerInfo,
			IsCoreNode: true,
			ChildCount: len(corenode.ChildMiners),
		}
	} else {
		minerInfo := s.model.GetMinerInfo(pubkey)
		if minerInfo != nil {
			resp.Data = &localwire.MinerInfo{
				MinerInfo:  minerInfo,
				IsCoreNode: false,
				ChildCount: 0,
			}
		} else {
			resp.Data = nil
			resp.Code = -1
			resp.Msg = "not found"
		}
	}

	c.JSON(http.StatusOK, resp)
}

// include plain sats
func (s *Handle) getAssetSummaryV3(c *gin.Context) {
	resp := &indexerwire.AssetSummaryRespV3{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	address := c.Param("address")
	start, err := strconv.ParseInt(c.DefaultQuery("start", "0"), 10, 64)
	if err != nil {
		start = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", QueryParamDefaultLimit))
	if err != nil {
		limit = 100
	}

	result, err := s.model.GetAssetSummaryV3(address, int(start), limit)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = result
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getUtxosWithTickerV3(c *gin.Context) {
	resp := &indexerwire.UtxosWithAssetRespV3{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	address := c.Param("address")
	ticker := c.Param("ticker")
	start, err := strconv.ParseInt(c.DefaultQuery("start", "0"), 10, 64)
	if err != nil {
		start = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", QueryParamDefaultLimit))
	if err != nil {
		limit = 100
	}

	result, total, err := s.model.GetUtxosWithAssetNameV3(address, ticker, int(start), limit)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.ListResp = indexerwire.ListResp{
		Total: uint64(total),
		Start: start,
	}
	resp.Data = result

	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getUtxoInfoV3(c *gin.Context) {
	resp := &indexerwire.TxOutputRespV3{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}

	utxo := c.Param("utxo")
	result, err := s.model.GetUtxoInfoV3(utxo)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = result
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getUtxoInfoListV3(c *gin.Context) {
	resp := &indexerwire.TxOutputListRespV3{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
	}

	var req indexerwire.UtxosReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	result, err := s.model.GetUtxoInfoListV3(&req)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
	} else {
		resp.Data = result
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getContracts(c *gin.Context) {
	resp := &localwire.ContractListResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
	}
	resp.Contracts = s.model.GetSupportedContracts()

	query := parseContractListQuery(c)
	if !query.requiresFullList() {
		resp.Data, resp.Total = s.model.GetContracts(query.start, query.limit)
		resp.ContractURLs, _ = s.model.GetDeployedContracts(query.start, query.limit)
		c.JSON(http.StatusOK, resp)
		return
	}

	contracts, _ := s.model.GetContracts(0, 0)
	contracts = filterContractSummaries(contracts, query)
	sortContractSummaries(contracts, query)

	resp.Total = len(contracts)
	resp.Data = paginateContractSummaries(contracts, query.start, query.limit)
	resp.ContractURLs = contractURLsFromSummaries(resp.Data)
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getContract(c *gin.Context) {
	summary, err := s.model.GetContract(c.Param("contract"))
	s.contractStatusJSON(c, summary, err)
}

func (s *Handle) getContractState(c *gin.Context) {
	s.contractRPC(c, "getcontractstate", []interface{}{c.Param("contract")})
}

func (s *Handle) getContractHistory(c *gin.Context) {
	resp := &localwire.ContractHistoryResp{
		BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
	}
	start, limit, err := parseContractHistoryWindow(c)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data, resp.Total, err = s.model.GetContractHistory(c.Param("contract"), start, limit)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	encoded, err := json.Marshal(resp.Data)
	if err == nil {
		resp.Status = string(encoded)
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) contractRPC(c *gin.Context, method string, params []interface{}) {
	resp := &localwire.ContractResp{
		BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
	}
	result, err := satsnet_rpc.Call(method, params)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = result
	resp.Status = string(result)
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getContractAnalytics(c *gin.Context) {
	analytics, err := s.model.GetContractAnalytics(c.Param("contract"))
	s.contractStatusJSON(c, analytics, err)
}

func (s *Handle) getContractInvokeItem(c *gin.Context) {
	item, err := s.model.GetContractInvokeItemByInUtxo(c.Param("contract"), c.Param("inutxo"))
	s.contractStatusJSON(c, item, err)
}

func (s *Handle) getContractUsers(c *gin.Context) {
	start, limit := parseStartLimit(c)
	users, _, err := s.model.GetContractAllAddresses(c.Param("contract"), start, limit)
	s.contractStatusJSON(c, users, err)
}

func (s *Handle) getContractUser(c *gin.Context) {
	status, err := s.model.GetContractUserStatus(c.Param("contract"), c.Param("address"))
	s.contractStatusJSON(c, status, err)
}

func (s *Handle) getContractUserHistory(c *gin.Context) {
	start, limit := parseStartLimit(c)
	history, _, err := s.model.GetContractHistoryByAddress(c.Param("contract"), c.Param("address"), start, limit)
	s.contractStatusJSON(c, history, err)
}

func parseStartLimit(c *gin.Context) (int, int) {
	start, err := strconv.Atoi(c.DefaultQuery("start", "0"))
	if err != nil {
		start = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", QueryParamDefaultLimit))
	if err != nil {
		limit = 100
	}
	return start, limit
}

type contractListQuery struct {
	start          int
	limit          int
	contractTypeID *int
	contractType   string
	subtype        string
	sortBy         string
	desc           bool
}

func (q contractListQuery) requiresFullList() bool {
	return q.contractTypeID != nil || q.contractType != "" || q.subtype != "" || q.sortBy != ""
}

func parseContractListQuery(c *gin.Context) contractListQuery {
	start, limit := parseStartLimit(c)
	query := contractListQuery{
		start:        start,
		limit:        limit,
		contractType: strings.ToLower(strings.TrimSpace(firstQuery(c, "contract_type", "contractType"))),
		subtype:      strings.ToLower(strings.TrimSpace(firstQuery(c, "subtype", "sub_type", "subType"))),
		sortBy:       normalizeContractSortField(firstQuery(c, "sort", "sort_by", "sortBy")),
	}

	if value := firstQuery(c, "contract_type_id", "contractTypeId", "type_id", "typeId"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			query.contractTypeID = &parsed
		}
	}

	order := strings.ToLower(strings.TrimSpace(firstQuery(c, "order", "sort_order", "sortOrder")))
	query.desc = order == "desc" || order == "-1"
	return query
}

func normalizeContractSortField(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "created_height", "createdheight", "deploy_height", "deployheight", "deploy_time", "deploytime":
		return "created_height"
	case "updated_height", "updatedheight":
		return "updated_height"
	case "address":
		return "address"
	case "name":
		return "name"
	default:
		return ""
	}
}

func filterContractSummaries(contracts []contractcommon.ContractSummary, query contractListQuery) []contractcommon.ContractSummary {
	if query.contractTypeID == nil && query.contractType == "" && query.subtype == "" {
		return contracts
	}

	filtered := make([]contractcommon.ContractSummary, 0, len(contracts))
	for _, contract := range contracts {
		if query.contractTypeID != nil && int(contract.ContractTypeID) != *query.contractTypeID {
			continue
		}
		if query.contractType != "" && strings.ToLower(contract.ContractType) != query.contractType {
			continue
		}
		if query.subtype != "" && strings.ToLower(contract.Subtype) != query.subtype {
			continue
		}
		filtered = append(filtered, contract)
	}
	return filtered
}

func sortContractSummaries(contracts []contractcommon.ContractSummary, query contractListQuery) {
	if query.sortBy == "" {
		return
	}

	sort.SliceStable(contracts, func(i, j int) bool {
		left := contracts[i]
		right := contracts[j]
		less := contractSummaryLess(left, right, query.sortBy)
		if query.desc {
			return contractSummaryLess(right, left, query.sortBy)
		}
		return less
	})
}

func contractSummaryLess(left, right contractcommon.ContractSummary, sortBy string) bool {
	switch sortBy {
	case "created_height":
		if left.CreatedHeight != right.CreatedHeight {
			return left.CreatedHeight < right.CreatedHeight
		}
	case "updated_height":
		if left.UpdatedHeight != right.UpdatedHeight {
			return left.UpdatedHeight < right.UpdatedHeight
		}
	case "name":
		if left.Name != right.Name {
			return left.Name < right.Name
		}
	}
	return left.Address < right.Address
}

func paginateContractSummaries(contracts []contractcommon.ContractSummary, start, limit int) []contractcommon.ContractSummary {
	total := len(contracts)
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = total
	}
	if start >= total {
		return nil
	}
	end := start + limit
	if end > total {
		end = total
	}
	return contracts[start:end]
}

func contractURLsFromSummaries(contracts []contractcommon.ContractSummary) []string {
	urls := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		urls = append(urls, contract.Address)
	}
	return urls
}

func parseContractHistoryWindow(c *gin.Context) (int, int, error) {
	start := 0
	limit := 100
	if skip := firstQuery(c, "skip", "start"); skip != "" {
		n, err := strconv.Atoi(skip)
		if err != nil {
			return 0, 0, err
		}
		start = n
	}
	if count := firstQuery(c, "count", "limit"); count != "" {
		n, err := strconv.Atoi(count)
		if err != nil {
			return 0, 0, err
		}
		limit = n
	}
	return start, limit, nil
}

func (s *Handle) contractStatusJSON(c *gin.Context, data any, err error) {
	resp := &localwire.ContractResp{
		BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
	}
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Status = string(encoded)
	resp.Data = encoded
	c.JSON(http.StatusOK, resp)
}

func firstQuery(c *gin.Context, names ...string) string {
	for _, name := range names {
		if value := c.Query(name); value != "" {
			return value
		}
	}
	return ""
}
