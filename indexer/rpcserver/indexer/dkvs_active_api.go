package indexer

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type activeDKVSBackend interface {
	GetDKVSActivePage(context.Context, dkvs.ActiveSyncRequest) (*dkvs.ActivePage, error)
	WatchDKVSActive(context.Context, dkvs.ActiveWatchRequest) (*dkvs.ActiveWatchResult, error)
}

type activeDKVSResponse struct {
	indexerwire.BaseResp
	ErrorCode string `json:"error_code,omitempty"`
	Data any `json:"data,omitempty"`
}

func (s *Handle) activeDKVSResponse(c *gin.Context, data any, err error) {
	if c.Request.Context().Err() != nil { return }
	response := &activeDKVSResponse{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}, Data: data}
	if err != nil {
		response.Data = nil
		setDKVSError(&response.BaseResp, &response.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), response)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, response)
}

func (s *Handle) getDKVSActivePage(c *gin.Context) {
	var request dkvs.ActiveSyncRequest
	if err := bindDKVSJSON(c, &request, 256*1024); err != nil { s.activeDKVSResponse(c, nil, err); return }
	backend, ok := s.model.indexer.(activeDKVSBackend)
	if !ok { s.activeDKVSResponse(c, nil, dkvs.ErrResetRequired); return }
	page, err := backend.GetDKVSActivePage(c.Request.Context(), request)
	s.activeDKVSResponse(c, page, err)
}

func (s *Handle) watchDKVSActive(c *gin.Context) {
	var request dkvs.ActiveWatchRequest
	if err := bindDKVSJSON(c, &request, 256*1024); err != nil { s.activeDKVSResponse(c, nil, err); return }
	backend, ok := s.model.indexer.(activeDKVSBackend)
	if !ok { s.activeDKVSResponse(c, nil, dkvs.ErrResetRequired); return }
	result, err := backend.WatchDKVSActive(c.Request.Context(), request)
	s.activeDKVSResponse(c, result, err)
}
