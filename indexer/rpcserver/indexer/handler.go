package indexer

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/indexer/common"
	localwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
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
		IsCoreNdoe: false,
	}

	pubkey := c.Param("pubkey")
	resp.IsCoreNdoe, resp.Childs = s.model.GetCoreNodeInfo(pubkey)

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
