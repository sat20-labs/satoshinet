package satoshinet

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
)

// @Summary send Raw Transaction
// @Description send Raw Transaction
// @Tags ordx.btc
// @Produce json
// @Param signedTxHex body string true "Signed transaction hex"
// @Param maxfeerate body number false "Reject transactions whose fee rate is higher than the specified value, expressed in BTC/kB.default:"0.01"
// @Security Bearer
// @Success 200 {object} SendRawTxResp "Successful response"
// @Failure 401 "Invalid API Key"
// @Router /btc/tx [post]
func (s *Service) sendRawTx(c *gin.Context) {
	resp := &indexerwire.SendRawTxResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: "",
	}
	var req indexerwire.SendRawTxReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	txid, err := satsnet_rpc.SendRawTransaction(req.SignedTxHex, req.Maxfeerate != 0)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = txid.String()
	c.JSON(http.StatusOK, resp)
}


func (s *Service) testRawTx(c *gin.Context) {
	resp := &indexerwire.TestRawTxResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
	}
	var req indexerwire.TestRawTxReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	result, err := satsnet_rpc.TestRawTransaction(req.SignedTxs)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	
	for _, r := range result {
		resp.Data = append(resp.Data, &indexerwire.TxTestResult{
			TxId: r.Txid,
			Allowed: r.Allowed,
			RejectReason: r.RejectReason,
		})
	}
	c.JSON(http.StatusOK, resp)
}


// @Summary get raw block with blockhash
// @Description get raw block with blockhash
// @Tags ordx.btc
// @Produce json
// @Param blockHash path string true "blockHash"
// @Security Bearer
// @Success 200 {object} RawBlockResp "Successful response"
// @Failure 401 "Invalid API Key"
// @Router /btc/block/{blockhash} [get]
func (s *Service) getRawBlock(c *gin.Context) {
	resp := &indexerwire.RawBlockResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: "",
	}
	blockHash := c.Param("blockhash")
	hash, err := chainhash.NewHashFromStr(blockHash)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}
	data, err := satsnet_rpc.GetRawBlock(hash)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	str, err := satsnet_rpc.EncodeMsgBlockToString(data)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = str
	c.JSON(http.StatusOK, resp)
}

// @Summary get block hash with height
// @Description get block hash with height
// @Tags ordx.btc
// @Produce json
// @Param height path string true "height"
// @Security Bearer
// @Success 200 {object} BlockHashResp "Successful response"
// @Failure 401 "Invalid API Key"
// @Router /btc/block/blockhash/{height} [get]
func (s *Service) getBlockHash(c *gin.Context) {
	resp := &indexerwire.BlockHashResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: "",
	}
	height, err := strconv.ParseInt(c.Param("height"), 10, 64)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	data, err := satsnet_rpc.GetBlockHash(height)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = data.String()
	c.JSON(http.StatusOK, resp)
}

func (s *Service) getTxSimpleInfo(c *gin.Context) {
	resp := &indexerwire.TxSimpleInfoResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}
	txid := c.Param("txid")
	tx, err := satsnet_rpc.GetTxVerbose(txid)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	block, err := satsnet_rpc.GetRawBlockVerbose(tx.BlockHash)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	txInfo := &indexerwire.TxSimpleInfo{
		TxID:          tx.Txid,
		Version:       tx.Version,
		Confirmations: tx.Confirmations,
		BlockHeight:   block.Height,
		BlockTime:     tx.Blocktime,
	}

	resp.Data = txInfo
	c.JSON(http.StatusOK, resp)
}

// @Summary get raw tx with txid
// @Description get raw tx with txid
// @Tags ordx.btc
// @Produce json
// @Param txid path string true "txid"
// @Security Bearer
// @Success 200 {object} TxResp "Successful response"
// @Failure 401 "Invalid API Key"
// @Router /btc/rawtx/{txid} [get]
func (s *Service) getRawTx(c *gin.Context) {
	resp := &indexerwire.TxResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: nil,
	}
	txid := c.Param("txid")
	rawtx, err := satsnet_rpc.GetTx(txid)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	str, err := satsnet_rpc.EncodeTxToString(rawtx)
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = str
	c.JSON(http.StatusOK, resp)
}

// @Summary get best block height
// @Description get best block height
// @Tags ordx.btc
// @Produce json
// @Security Bearer
// @Success 200 {object} BestBlockHeightResp "Successful response"
// @Failure 401 "Invalid API Key"
// @Router /btc/block/bestblockheight [get]
func (s *Service) getBestBlockHeight(c *gin.Context) {
	resp := &indexerwire.BestBlockHeightResp{
		BaseResp: indexerwire.BaseResp{
			Code: 0,
			Msg:  "ok",
		},
		Data: -1,
	}

	_, height, err := satsnet_rpc.GetBestBlock()
	if err != nil {
		resp.Code = -1
		resp.Msg = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.Data = int64(height)
	c.JSON(http.StatusOK, resp)
}
