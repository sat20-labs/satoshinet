package indexer

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	localwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const QueryParamDefaultLimit = "100"

const (
	dkvsRecordHTTPBodyLimit       = int64(32 * 1024)
	dkvsSubscriptionHTTPBodyLimit = int64(4 * 1024)
	dkvsSnapshotHTTPBodyLimit     = int64(64 * 1024 * 1024)
	dkvsMaxListLimit              = 1000
)

func bindDKVSJSON(c *gin.Context, target interface{}, limit int64) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	return c.ShouldBindJSON(target)
}

func defaultEVMCompilerConfig() contractcommon.EVMCompilerConfig {
	var cfg contractcommon.EVMCompilerConfig
	cfg.SolcVersion = "0.8.30"
	cfg.EVMVersion = "paris"
	cfg.Optimizer.Enabled = true
	cfg.Optimizer.Runs = 200
	cfg.Metadata.BytecodeHash = "none"
	cfg.SingleFileOnly = true
	cfg.AllowImports = false
	return cfg
}

func contractRespSetData(resp *localwire.ContractResp, data interface{}) {
	encoded, err := json.Marshal(data)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		return
	}
	resp.Data = json.RawMessage(encoded)
}

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

type dkvsRecordResp struct {
	indexerwire.BaseResp
	Data *swire.DKVSRecord `json:"data,omitempty"`
}

type dkvsRecordsResp struct {
	indexerwire.BaseResp
	Total int                 `json:"total"`
	Data  []*swire.DKVSRecord `json:"data,omitempty"`
}

type dkvsCheckpointResp struct {
	indexerwire.BaseResp
	Data interface{} `json:"data,omitempty"`
}

type dkvsUsageResp struct {
	indexerwire.BaseResp
	Data *dkvsindexer.Usage `json:"data,omitempty"`
}

type dkvsConfigResp struct {
	indexerwire.BaseResp
	Data *dkvsindexer.FreeLocalCachePolicy `json:"data,omitempty"`
}

type dkvsPathMetaResp struct {
	indexerwire.BaseResp
	Data *dkvsindexer.PathMeta `json:"data,omitempty"`
}

type dkvsSubscriptionReq struct {
	Type   dkvsindexer.SubscriptionType `json:"type"`
	Target string                       `json:"target"`
}

type dkvsSubscriptionResp struct {
	indexerwire.BaseResp
	Total         int                        `json:"total,omitempty"`
	Subscriptions []dkvsindexer.Subscription `json:"subscriptions,omitempty"`
	Data          []*swire.DKVSRecord        `json:"data,omitempty"`
}

type dkvsPruneResp struct {
	indexerwire.BaseResp
	Pruned int `json:"pruned"`
}

type dkvsSnapshotImportResp struct {
	indexerwire.BaseResp
	Applied int `json:"applied"`
}

func (s *Handle) putDKVSRecord(c *gin.Context) {
	resp := &dkvsRecordResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var record swire.DKVSRecord
	if err := bindDKVSJSON(c, &record, dkvsRecordHTTPBodyLimit); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	if _, err := s.model.PutDKVSRecord(&record); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = &record
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) putDKVSTombstone(c *gin.Context) {
	resp := &dkvsRecordResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var record swire.DKVSRecord
	if err := bindDKVSJSON(c, &record, dkvsRecordHTTPBodyLimit); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	if record.Flags&1 == 0 || len(record.Value) != 0 {
		resp.Code = -1
		resp.Msg = "invalid dkvs tombstone record"
		c.JSON(http.StatusOK, resp)
		return
	}
	if _, err := s.model.PutDKVSRecord(&record); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = &record
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDKVSRecord(c *gin.Context) {
	resp := &dkvsRecordResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	key := c.Query("key")
	var record *swire.DKVSRecord
	var err error
	if key != "" {
		record, err = s.model.GetDKVSRecord(key)
	} else {
		hashParam := c.Query("hash")
		hash, hashErr := chainhash.NewHashFromStr(hashParam)
		if hashErr != nil {
			resp.Code = -1
			resp.Msg = hashErr.Error()
			c.JSON(http.StatusOK, resp)
			return
		}
		record, err = s.model.GetDKVSRecordByHash(*hash)
	}
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = record
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) listDKVSRecords(c *gin.Context) {
	resp := &dkvsRecordsResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	start, err := strconv.Atoi(c.DefaultQuery("start", "0"))
	if err != nil {
		start = 0
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", QueryParamDefaultLimit))
	if err != nil {
		limit = 100
	}
	if limit > dkvsMaxListLimit {
		limit = dkvsMaxListLimit
	}
	records, total, err := s.model.ListDKVSRecords(c.Query("prefix"), start, limit)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Total = total
	resp.Data = records
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDKVSUsage(c *gin.Context) {
	resp := &dkvsUsageResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	usage, err := s.model.GetDKVSUsage(c.Query("prefix"))
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = usage
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDKVSConfig(c *gin.Context) {
	resp := &dkvsConfigResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	policy := s.model.GetDKVSFreeLocalCachePolicy()
	resp.Data = &policy
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDKVSPathMeta(c *gin.Context) {
	resp := &dkvsPathMetaResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	meta, err := s.model.GetDKVSPathMeta(c.Query("path"))
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	resp.Data = meta
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDKVSCheckpoint(c *gin.Context) {
	resp := &dkvsCheckpointResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	checkpoint, err := s.model.GetDKVSCheckpoint()
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = checkpoint
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDKVSSnapshot(c *gin.Context) {
	resp := &dkvsCheckpointResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	snapshot, err := s.model.GetDKVSSnapshot()
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = snapshot
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) applyDKVSSnapshot(c *gin.Context) {
	resp := &dkvsSnapshotImportResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var snapshot dkvsindexer.Snapshot
	if err := bindDKVSJSON(c, &snapshot, dkvsSnapshotHTTPBodyLimit); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	applied, err := s.model.ApplyDKVSSnapshot(&snapshot)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Applied = applied
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) pruneDKVS(c *gin.Context) {
	resp := &dkvsPruneResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	pruned, err := s.model.PruneExpiredDKVSRecords()
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Pruned = pruned
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) subscribeDKVS(c *gin.Context) {
	resp := &dkvsSubscriptionResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsSubscriptionReq
	if err := bindDKVSJSON(c, &req, dkvsSubscriptionHTTPBodyLimit); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	records, total, err := s.model.SubscribeDKVS(dkvsindexer.Subscription{Type: req.Type, Target: req.Target})
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Total = total
	resp.Data = records
	resp.Subscriptions = s.model.ListDKVSSubscriptions()
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) unsubscribeDKVS(c *gin.Context) {
	resp := &dkvsSubscriptionResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsSubscriptionReq
	if err := bindDKVSJSON(c, &req, dkvsSubscriptionHTTPBodyLimit); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	if err := s.model.UnsubscribeDKVS(dkvsindexer.Subscription{Type: req.Type, Target: req.Target}); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Subscriptions = s.model.ListDKVSSubscriptions()
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) listDKVSSubscriptions(c *gin.Context) {
	resp := &dkvsSubscriptionResp{
		BaseResp:      indexerwire.BaseResp{Code: 0, Msg: "ok"},
		Subscriptions: s.model.ListDKVSSubscriptions(),
	}
	resp.Total = len(resp.Subscriptions)
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
	if len(req.PubKey) == 0 || len(req.Sig) == 0 {
		resp.Code = -1
		resp.Msg = "pubkey and msgSig are required"
		c.JSON(http.StatusOK, resp)
		return
	}
	if !s.model.CheckCoreNode(hex.EncodeToString(req.PubKey)) {
		resp.Code = -1
		resp.Msg = "only core node can record channel state event"
		c.JSON(http.StatusOK, resp)
		return
	}
	msg, err := json.Marshal(req.Data)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	if err := indexercommon.VerifySignOfMessage(msg, req.Sig, req.PubKey); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
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

func (s *Handle) getAddressUtxosV3(c *gin.Context) {
	resp := &indexerwire.UtxosWithAssetRespV3{
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

	result, total, err := s.model.GetAddressUtxosV3(address, int(start), limit)
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
	contracts, _ := s.model.GetContracts(0, 0)
	contracts = filterRuntimeExistingContractSummaries(contracts, query)
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

func (s *Handle) getEVMCompilerConfig(c *gin.Context) {
	resp := &localwire.ContractResp{
		BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
	}
	contractRespSetData(resp, defaultEVMCompilerConfig())
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getEVMSourceMetadata(c *gin.Context) {
	resp := &localwire.ContractResp{
		BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
	}
	contractAddress := strings.TrimSpace(c.Param("contract"))
	metadata, ok := s.model.GetEVMSourceMetadata(contractAddress)
	if !ok {
		resp.Code = -1
		resp.Msg = "EVM source metadata not found"
		c.JSON(http.StatusOK, resp)
		return
	}
	contractRespSetData(resp, metadata)
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) putEVMSourceMetadata(c *gin.Context) {
	resp := &localwire.ContractResp{
		BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
	}
	contractAddress := strings.TrimSpace(c.Param("contract"))
	summary, err := s.model.GetContract(contractAddress)
	if err == nil && summary.ContractTypeID != contractcommon.ContractTypeEVM {
		resp.Code = -1
		resp.Msg = "contract is not EVM"
		c.JSON(http.StatusOK, resp)
		return
	}
	if err != nil {
		contractAddr, decodeErr := contractcommon.DecodeContractAddress(contractAddress)
		if decodeErr != nil || contractAddr.ContractType() != contractcommon.ContractTypeEVM {
			resp.Code = -1
			resp.Msg = err.Error()
			c.JSON(http.StatusOK, resp)
			return
		}
	}
	var req contractcommon.EVMSourceMetadata
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	req.ContractAddress = contractAddress
	if strings.TrimSpace(req.ContractName) == "" {
		resp.Code = -1
		resp.Msg = "missing contractName"
		c.JSON(http.StatusOK, resp)
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		resp.Code = -1
		resp.Msg = "missing source"
		c.JSON(http.StatusOK, resp)
		return
	}
	if len(req.ABI) > 0 && !json.Valid(req.ABI) {
		resp.Code = -1
		resp.Msg = "invalid ABI JSON"
		c.JSON(http.StatusOK, resp)
		return
	}
	req.CompilerConfig = defaultEVMCompilerConfig()
	req.Verified = false
	req.VerifyStatus = "stored"
	req.VerifyError = "server-side Solidity recompilation is not enabled"
	now := time.Now().Unix()
	if req.SubmittedAt == 0 {
		req.SubmittedAt = now
	}
	req.UpdatedAt = now
	if err := s.model.PutEVMSourceMetadata(req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	contractRespSetData(resp, req)
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) estimateEVMDeploy(c *gin.Context) {
	resp := &localwire.ContractResp{
		BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
	}
	var req struct {
		Caller      string  `json:"caller"`
		InitCodeHex string  `json:"initCodeHex"`
		Value       *int64  `json:"value"`
		Sats        *int64  `json:"sats"`
		GasLimit    *int64  `json:"gasLimit"`
		DeployNonce *uint64 `json:"deployNonce"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	value := req.Value
	if value == nil {
		value = req.Sats
	}
	param := map[string]interface{}{
		"caller":      req.Caller,
		"initCodeHex": req.InitCodeHex,
	}
	if value != nil {
		param["value"] = *value
	}
	if req.GasLimit != nil {
		param["gasLimit"] = *req.GasLimit
	}
	if req.DeployNonce != nil {
		param["deployNonce"] = *req.DeployNonce
	}
	result, err := contractStateCall("estimateevmdeploy", []interface{}{param})
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = json.RawMessage(result)
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) estimateEVMInvoke(c *gin.Context) {
	resp := &localwire.ContractResp{
		BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
	}
	var req struct {
		Caller      string `json:"caller"`
		CalldataHex string `json:"calldataHex"`
		Value       *int64 `json:"value"`
		Sats        *int64 `json:"sats"`
		GasLimit    *int64 `json:"gasLimit"`
		Funding     []struct {
			AssetName string `json:"assetName"`
			Amount    string `json:"amount"`
		} `json:"funding"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	value := req.Value
	if value == nil {
		value = req.Sats
	}
	funding := make([]interface{}, 0, len(req.Funding))
	for _, item := range req.Funding {
		funding = append(funding, map[string]interface{}{
			"assetName": item.AssetName,
			"amount":    item.Amount,
		})
	}
	param := map[string]interface{}{
		"contractAddress": c.Param("contract"),
		"caller":          req.Caller,
		"calldataHex":     req.CalldataHex,
		"funding":         funding,
	}
	if value != nil {
		param["value"] = *value
	}
	if req.GasLimit != nil {
		param["gasLimit"] = *req.GasLimit
	}
	result, err := contractStateCall("estimateevminvoke", []interface{}{param})
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Data = json.RawMessage(result)
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) reviewPredictionReady(c *gin.Context) {
	resp := &localwire.ContractResp{
		BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
	}
	var req struct {
		ContractJSON string          `json:"contractJson"`
		Content      string          `json:"content"`
		Contract     json.RawMessage `json:"contract"`
		Prediction   json.RawMessage `json:"prediction"`
		CheckedAt    *int64          `json:"checkedAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	contractJSON := strings.TrimSpace(req.ContractJSON)
	if contractJSON == "" {
		contractJSON = strings.TrimSpace(req.Content)
	}
	if contractJSON == "" && len(req.Contract) > 0 {
		contractJSON = string(req.Contract)
	}
	if contractJSON == "" && len(req.Prediction) > 0 {
		contractJSON = string(req.Prediction)
	}
	if contractJSON == "" {
		resp.Code = -1
		resp.Msg = "missing prediction contract json"
		c.JSON(http.StatusOK, resp)
		return
	}
	params := []interface{}{contractJSON}
	if req.CheckedAt != nil && *req.CheckedAt > 0 {
		params = append(params, *req.CheckedAt)
	}
	result, err := contractStateCall("reviewpredictionready", params)
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
	includeInvalid bool
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
	query.includeInvalid = parseBoolQuery(c, "include_invalid", "includeInvalid", "all")

	if value := firstQuery(c, "contract_type_id", "contractTypeId", "type_id", "typeId"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			query.contractTypeID = &parsed
		}
	}

	order := strings.ToLower(strings.TrimSpace(firstQuery(c, "order", "sort_order", "sortOrder")))
	query.desc = order == "desc" || order == "-1"
	return query
}

var contractStateCall = satsnet_rpc.Call

func filterRuntimeExistingContractSummaries(contracts []contractcommon.ContractSummary, query contractListQuery) []contractcommon.ContractSummary {
	if query.includeInvalid {
		return contracts
	}

	filtered := make([]contractcommon.ContractSummary, 0, len(contracts))
	for _, contract := range contracts {
		if !contractSummaryStatusActive(contract.Status) {
			continue
		}
		exists, err := contractRuntimeExists(contract.Address)
		if err != nil || exists {
			filtered = append(filtered, contract)
		}
	}
	return filtered
}

func contractSummaryStatusActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "invalid", "revert", "reverted", "out_of_gas", "closed", "rejected":
		return false
	default:
		return true
	}
}

func contractRuntimeExists(address string) (bool, error) {
	if strings.TrimSpace(address) == "" {
		return true, nil
	}

	result, err := contractStateCall("getcontractstate", []interface{}{address})
	if err != nil {
		return true, err
	}
	return contractStateResponseExists(result)
}

func contractStateResponseExists(result []byte) (bool, error) {
	var state struct {
		Details map[string]interface{} `json:"details"`
	}
	if err := json.Unmarshal(result, &state); err != nil {
		return true, err
	}
	exists, ok := state.Details["exists"]
	if !ok {
		return true, nil
	}
	if value, ok := exists.(bool); ok {
		return value, nil
	}
	return true, nil
}

func parseBoolQuery(c *gin.Context, names ...string) bool {
	switch strings.ToLower(strings.TrimSpace(firstQuery(c, names...))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
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
