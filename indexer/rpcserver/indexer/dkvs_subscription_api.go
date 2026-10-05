package indexer

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type dkvsPrefixReadReq struct {
	Prefix string `json:"prefix"`
}

type dkvsPrefixReadResp struct {
	indexerwire.BaseResp
	ErrorCode string                        `json:"error_code,omitempty"`
	Data      *dkvsindexer.PrefixReadResult `json:"data,omitempty"`
}

type dkvsSubscriptionBackend interface {
	ReadDKVSPrefix(string) (*dkvsindexer.PrefixReadResult, error)
}

func (s *Handle) dkvsSubscriptionBackend() (dkvsSubscriptionBackend, error) {
	backend, ok := s.model.indexer.(dkvsSubscriptionBackend)
	if !ok {
		return nil, errors.New("dkvs subscription backend is not available")
	}
	return backend, nil
}

func (s *Handle) readDKVSPrefix(c *gin.Context) {
	resp := &dkvsPrefixReadResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsPrefixReadReq
	if err := bindDKVSJSON(c, &req, dkvsSyncHTTPBodyLimit); err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	backend, err := s.dkvsSubscriptionBackend()
	if err == nil {
		resp.Data, err = backend.ReadDKVSPrefix(req.Prefix)
	}
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}
