package indexer

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type dkvsCASMutationReq struct {
	Record       *swire.DKVSRecord `json:"record"`
	ExpectedETag string            `json:"expected_etag,omitempty"`
	ExpectAbsent bool              `json:"expect_absent,omitempty"`
}

type dkvsBatchCASReq struct {
	Mutations     []dkvsCASMutationReq                  `json:"mutations"`
	EndpointID    string                                `json:"endpoint_id,omitempty"`
	RequestID     string                                `json:"request_id,omitempty"`
	Authorization *dkvsindexer.WalletWriteAuthorization `json:"authorization,omitempty"`
}

type dkvsBatchCASResp struct {
	indexerwire.BaseResp
	ErrorCode string                   `json:"error_code,omitempty"`
	Data      *dkvsindexer.WriteResult `json:"data,omitempty"`
}

type dkvsApplicationRecordResp struct {
	indexerwire.BaseResp
	ErrorCode string            `json:"error_code,omitempty"`
	Data      *swire.DKVSRecord `json:"data,omitempty"`
	ETag      string            `json:"etag,omitempty"`
}

type dkvsKeyStateResp struct {
	indexerwire.BaseResp
	ErrorCode string                    `json:"error_code,omitempty"`
	Data      *dkvsindexer.DKVSKeyState `json:"data,omitempty"`
}

type dkvsAuthorizedWalletBackend interface {
	PutDKVSRecordBatchCASAuthorized([]dkvsindexer.CASMutation, dkvsindexer.BatchCASOptions, *dkvsindexer.WalletWriteAuthorization) (*dkvsindexer.WriteResult, error)
}

func parseDKVSPrecondition(expectedETag string, absent bool) (dkvsindexer.WritePrecondition, error) {
	expectedETag = strings.TrimSpace(expectedETag)
	if absent == (expectedETag != "") {
		return dkvsindexer.WritePrecondition{}, dkvsindexer.ErrInvalidRecord
	}
	condition := dkvsindexer.WritePrecondition{ExpectAbsent: absent}
	if expectedETag != "" {
		hash, err := chainhash.NewHashFromStr(expectedETag)
		if err != nil {
			return condition, dkvsindexer.ErrInvalidRecord
		}
		condition.ExpectedHash = hash
	}
	return condition, nil
}

func dkvsHTTPStatus(err error) int {
	switch dkvsindexer.ErrorCodeOf(err) {
	case dkvsindexer.ErrorCodeWriteConflict, dkvsindexer.ErrorCodeStaleGeneration,
		dkvsindexer.ErrorCodeStaleEndpoint, dkvsindexer.ErrorCodeEndpointMismatch,
		dkvsindexer.ErrorCodeResetRequired, dkvsindexer.ErrorCodePathDiverged,
		dkvsindexer.ErrorCodeStorageModeDowngrade:
		return http.StatusConflict
	case dkvsindexer.ErrorCodePermissionDenied:
		return http.StatusForbidden
	case dkvsindexer.ErrorCodeQuotaExceeded:
		return http.StatusTooManyRequests
	case dkvsindexer.ErrorCodeRecordNotFound:
		return http.StatusNotFound
	default:
		return http.StatusBadRequest
	}
}

func setDKVSError(base *indexerwire.BaseResp, code *string, err error) {
	base.Code, base.Msg = -1, err.Error()
	*code = string(dkvsindexer.ErrorCodeOf(err))
}

func (s *Handle) putDKVSRecordBatchCAS(c *gin.Context) {
	resp := &dkvsBatchCASResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsBatchCASReq
	if err := bindDKVSJSON(c, &req, dkvsBatchHTTPBodyLimit); err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	if len(req.Mutations) == 0 || len(req.Mutations) > dkvsindexer.MaxBatchCASMutations {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, dkvsindexer.ErrBatchTooLarge)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	mutations := make([]dkvsindexer.CASMutation, 0, len(req.Mutations))
	for _, mutation := range req.Mutations {
		condition, err := parseDKVSPrecondition(mutation.ExpectedETag, mutation.ExpectAbsent)
		if err != nil || mutation.Record == nil {
			if err == nil {
				err = dkvsindexer.ErrInvalidRecord
			}
			setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
			c.JSON(http.StatusBadRequest, resp)
			return
		}
		mutations = append(mutations, dkvsindexer.CASMutation{Record: mutation.Record, Precondition: condition})
	}
	options := dkvsindexer.BatchCASOptions{EndpointID: strings.TrimSpace(req.EndpointID), RequestID: strings.TrimSpace(req.RequestID)}
	var err error
	backend, ok := s.model.indexer.(dkvsAuthorizedWalletBackend)
	if !ok {
		err = dkvsindexer.ErrPermissionDenied
	} else {
		resp.Data, err = backend.PutDKVSRecordBatchCASAuthorized(mutations, options, req.Authorization)
	}
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDKVSApplicationRecord(c *gin.Context) {
	resp := &dkvsApplicationRecordResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	key := strings.TrimSpace(c.Query("key"))
	if key == "" {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, dkvsindexer.ErrInvalidKey)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	record, err := s.model.GetDKVSRecord(key)
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	resp.Data, resp.ETag = record, dkvsindexer.RecordHash(record).String()
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDKVSKeyState(c *gin.Context) {
	resp := &dkvsKeyStateResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	key := strings.TrimSpace(c.Query("key"))
	if key == "" {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, dkvsindexer.ErrInvalidKey)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	state, err := s.model.indexer.GetDKVSKeyState(key)
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	resp.Data = &state
	c.JSON(http.StatusOK, resp)
}
