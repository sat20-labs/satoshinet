package indexer

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type dkvsCASMutationReq struct {
	Record           *swire.DKVSRecord        `json:"record"`
	ExpectedHash     string                   `json:"expected_hash,omitempty"`
	ExpectAbsent     bool                     `json:"expect_absent,omitempty"`
	PathPrecondition *dkvsPathPreconditionReq `json:"path_precondition,omitempty"`
	EndpointID       string                   `json:"endpoint_id,omitempty"`
}

type dkvsBatchCASReq struct {
	Mutations         []dkvsCASMutationReq      `json:"mutations"`
	PathPreconditions []dkvsPathPreconditionReq `json:"path_preconditions"`
	EndpointID        string                    `json:"endpoint_id,omitempty"`
}

type dkvsPathPreconditionReq struct {
	Path               string `json:"path"`
	ExpectedRoot       string `json:"expected_root"`
	ExpectedGeneration uint64 `json:"expected_generation"`
}

type dkvsV1ErrorResp struct {
	indexerwire.BaseResp
	ErrorCode string `json:"error_code,omitempty"`
}

type dkvsCASResp struct {
	indexerwire.BaseResp
	ErrorCode    string                           `json:"error_code,omitempty"`
	Data         *swire.DKVSRecord               `json:"data,omitempty"`
	Hash         string                           `json:"hash,omitempty"`
	Applied      int                              `json:"applied"`
	PathMeta     map[string]*dkvsindexer.PathMeta `json:"pathmeta,omitempty"`
	ServerTimeMS uint64                           `json:"server_time_ms"`
	LocalOnly    bool                             `json:"local_only,omitempty"`
	EndpointID   string                           `json:"endpoint_id,omitempty"`
}

type dkvsBatchCASResp struct {
	indexerwire.BaseResp
	ErrorCode string                   `json:"error_code,omitempty"`
	Data      *dkvsindexer.WriteResult `json:"data,omitempty"`
}

type dkvsDirectorySyncReq struct {
	Prefix string `json:"prefix"`
	Cursor []byte `json:"cursor,omitempty"`
	Limit  uint32 `json:"limit,omitempty"`
}

type dkvsDirectoryWatchReq struct {
	Prefix         string `json:"prefix"`
	Root           string `json:"root"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type dkvsPathSyncReq struct {
	Path string `json:"path"`
}

type dkvsPathMetaData struct {
	ServerTimeMS uint64                `json:"server_time_ms"`
	PathMeta     *dkvsindexer.PathMeta `json:"pathmeta"`
}

type dkvsPathMetaV1Resp struct {
	indexerwire.BaseResp
	ErrorCode string            `json:"error_code,omitempty"`
	Data      *dkvsPathMetaData `json:"data,omitempty"`
}

type dkvsPathSyncResp struct {
	indexerwire.BaseResp
	ErrorCode string                    `json:"error_code,omitempty"`
	Data      *dkvsindexer.PathSnapshot `json:"data,omitempty"`
}

type dkvsPathWatchReq struct {
	Path           string `json:"path"`
	Generation     uint64 `json:"generation"`
	StateRoot      string `json:"state_root"`
	ViewHeight     uint64 `json:"view_height,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type dkvsPathWatchData struct {
	Changed      bool                    `json:"changed"`
	ServerTimeMS uint64                  `json:"server_time_ms"`
	PathMeta     *dkvsindexer.PathMeta `json:"pathmeta"`
}

type dkvsPathWatchResp struct {
	indexerwire.BaseResp
	ErrorCode string             `json:"error_code,omitempty"`
	Data      *dkvsPathWatchData `json:"data,omitempty"`
}

type dkvsV1Backend interface {
	PutDKVSRecordCASResult(*swire.DKVSRecord, dkvsindexer.WritePrecondition, dkvsindexer.BatchCASOptions) (*dkvsindexer.WriteResult, error)
	PutDKVSRecordBatchCASResultWithOptions([]dkvsindexer.CASMutation, dkvsindexer.BatchCASOptions) (*dkvsindexer.WriteResult, error)
	GetDKVSPathSnapshot(string) (*dkvsindexer.PathSnapshot, error)
}

func (s *Handle) dkvsV1Backend() (dkvsV1Backend, error) {
	backend, ok := s.model.indexer.(dkvsV1Backend)
	if !ok {
		return nil, errors.New("dkvs v1 backend is not available")
	}
	return backend, nil
}

func parseDKVSPrecondition(expected string, absent bool) (dkvsindexer.WritePrecondition, error) {
	expected = strings.TrimSpace(expected)
	if absent == (expected != "") {
		return dkvsindexer.WritePrecondition{}, dkvsindexer.ErrInvalidRecord
	}
	condition := dkvsindexer.WritePrecondition{ExpectAbsent: absent}
	if expected != "" {
		hash, err := chainhash.NewHashFromStr(expected)
		if err != nil {
			return condition, dkvsindexer.ErrInvalidRecord
		}
		condition.ExpectedHash = hash
	}
	return condition, nil
}

func parseDKVSPathPreconditions(requests []dkvsPathPreconditionReq) ([]dkvsindexer.PathWritePrecondition, error) {
	conditions := make([]dkvsindexer.PathWritePrecondition, 0, len(requests))
	for _, request := range requests {
		root, err := chainhash.NewHashFromStr(strings.TrimSpace(request.ExpectedRoot))
		if err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		conditions = append(conditions, dkvsindexer.PathWritePrecondition{
			Path: strings.TrimSpace(request.Path), ExpectedRoot: *root,
			ExpectedGeneration: request.ExpectedGeneration,
		})
	}
	return conditions, nil
}

func dkvsHTTPStatus(err error) int {
	switch dkvsindexer.ErrorCodeOf(err) {
	case dkvsindexer.ErrorCodeWriteConflict, dkvsindexer.ErrorCodeStaleGeneration,
		dkvsindexer.ErrorCodeStaleEndpoint, dkvsindexer.ErrorCodePathDiverged:
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
	base.Code = -1
	base.Msg = err.Error()
	*code = string(dkvsindexer.ErrorCodeOf(err))
}

func (s *Handle) putDKVSRecordCAS(c *gin.Context) {
	resp := &dkvsCASResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsCASMutationReq
	if err := bindDKVSJSON(c, &req, dkvsRecordHTTPBodyLimit); err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	condition, err := parseDKVSPrecondition(req.ExpectedHash, req.ExpectAbsent)
	if err == nil && req.Record == nil {
		err = dkvsindexer.ErrInvalidRecord
	}
	var pathConditions []dkvsindexer.PathWritePrecondition
	if err == nil && dkvsindexer.RecordRequiresPathPrecondition(req.Record) {
		if req.PathPrecondition == nil {
			err = dkvsindexer.ErrStaleGeneration
		} else {
			pathConditions, err = parseDKVSPathPreconditions([]dkvsPathPreconditionReq{*req.PathPrecondition})
		}
	} else if err == nil && req.PathPrecondition != nil {
		err = dkvsindexer.ErrInvalidRecord
	}
	var result *dkvsindexer.WriteResult
	if err == nil {
		backend, backendErr := s.dkvsV1Backend()
		if backendErr != nil {
			err = backendErr
		} else {
			result, err = backend.PutDKVSRecordCASResult(req.Record, condition,
				dkvsindexer.BatchCASOptions{
					PathPreconditions: pathConditions,
					EndpointID: strings.TrimSpace(req.EndpointID),
				})
		}
	}
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	if result == nil || len(result.Records) != 1 || len(result.Hashes) != 1 {
		err = dkvsindexer.ErrInvalidRecord
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(http.StatusInternalServerError, resp)
		return
	}
	resp.Data = result.Records[0]
	resp.Hash = result.Hashes[0]
	resp.Applied = result.Applied
	resp.PathMeta = result.PathMeta
	resp.ServerTimeMS = result.ServerTimeMS
	resp.LocalOnly = result.LocalOnly
	resp.EndpointID = result.EndpointID
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) putDKVSRecordBatchCAS(c *gin.Context) {
	resp := &dkvsBatchCASResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsBatchCASReq
	if err := bindDKVSJSON(c, &req, dkvsBatchHTTPBodyLimit); err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	mutations := make([]dkvsindexer.CASMutation, 0, len(req.Mutations))
	requiresPathPrecondition := false
	for _, mutation := range req.Mutations {
		condition, err := parseDKVSPrecondition(mutation.ExpectedHash, mutation.ExpectAbsent)
		if err != nil || mutation.Record == nil {
			setDKVSError(&resp.BaseResp, &resp.ErrorCode, dkvsindexer.ErrInvalidRecord)
			c.JSON(http.StatusBadRequest, resp)
			return
		}
		if dkvsindexer.RecordRequiresPathPrecondition(mutation.Record) {
			requiresPathPrecondition = true
		}
		mutations = append(mutations, dkvsindexer.CASMutation{Record: mutation.Record, Precondition: condition})
	}
	pathConditions, err := parseDKVSPathPreconditions(req.PathPreconditions)
	if err == nil && requiresPathPrecondition && len(pathConditions) == 0 {
		err = dkvsindexer.ErrStaleGeneration
	}
	if err == nil && !requiresPathPrecondition && len(pathConditions) != 0 {
		err = dkvsindexer.ErrInvalidRecord
	}
	if err == nil {
		backend, backendErr := s.dkvsV1Backend()
		if backendErr != nil {
			err = backendErr
		} else {
			resp.Data, err = backend.PutDKVSRecordBatchCASResultWithOptions(mutations,
				dkvsindexer.BatchCASOptions{
					PathPreconditions: pathConditions,
					EndpointID: strings.TrimSpace(req.EndpointID),
				})
		}
	}
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) getDKVSPathMetaV1(c *gin.Context) {
	resp := &dkvsPathMetaV1Resp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	meta, err := s.model.GetDKVSPathMeta(c.Query("path"))
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	resp.Data = &dkvsPathMetaData{ServerTimeMS: uint64(time.Now().UnixMilli()), PathMeta: meta}
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) syncDKVSPath(c *gin.Context) {
	resp := &dkvsPathSyncResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsPathSyncReq
	if err := bindDKVSJSON(c, &req, dkvsSyncHTTPBodyLimit); err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	backend, err := s.dkvsV1Backend()
	if err == nil {
		resp.Data, err = backend.GetDKVSPathSnapshot(req.Path)
	}
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func pathMetaChanged(meta *dkvsindexer.PathMeta, req dkvsPathWatchReq, root chainhash.Hash) bool {
	if meta == nil {
		return true
	}
	return meta.Generation != req.Generation || meta.StateRoot != root ||
		(req.ViewHeight != 0 && meta.ViewHeight != req.ViewHeight)
}

func (s *Handle) watchDKVSPath(c *gin.Context) {
	resp := &dkvsPathWatchResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsPathWatchReq
	if err := bindDKVSJSON(c, &req, dkvsSyncHTTPBodyLimit); err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	root, err := chainhash.NewHashFromStr(strings.TrimSpace(req.StateRoot))
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, dkvsindexer.ErrInvalidRecord)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 || timeout > dkvsMaxWatchSeconds {
		timeout = dkvsMaxWatchSeconds
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(timeout)*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		meta, metaErr := s.model.GetDKVSPathMeta(req.Path)
		if metaErr != nil {
			setDKVSError(&resp.BaseResp, &resp.ErrorCode, metaErr)
			c.JSON(dkvsHTTPStatus(metaErr), resp)
			return
		}
		if pathMetaChanged(meta, req, *root) {
			resp.Data = &dkvsPathWatchData{Changed: true, ServerTimeMS: uint64(time.Now().UnixMilli()), PathMeta: meta}
			c.JSON(http.StatusOK, resp)
			return
		}
		select {
		case <-ctx.Done():
			resp.Data = &dkvsPathWatchData{Changed: false, ServerTimeMS: uint64(time.Now().UnixMilli()), PathMeta: meta}
			c.JSON(http.StatusOK, resp)
			return
		case <-ticker.C:
		}
	}
}

func (s *Handle) syncDKVSDirectory(c *gin.Context) {
	resp := &dkvsSyncResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsDirectorySyncReq
	if err := bindDKVSJSON(c, &req, dkvsSyncHTTPBodyLimit); err != nil {
		resp.Code, resp.Msg = -1, err.Error()
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	records, next, done, root, err := s.model.SyncDKVSDirectory(req.Prefix, req.Cursor, req.Limit)
	if err != nil {
		resp.Code, resp.Msg = -1, err.Error()
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	resp.Data = &dkvsSyncData{Records: records, NextCursor: next, Done: done, Root: root.String()}
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) watchDKVSDirectory(c *gin.Context) {
	resp := &dkvsWatchResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsDirectoryWatchReq
	if err := bindDKVSJSON(c, &req, dkvsSyncHTTPBodyLimit); err != nil {
		resp.Code, resp.Msg = -1, err.Error()
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	knownRoot, err := chainhash.NewHashFromStr(strings.TrimSpace(req.Root))
	if err != nil {
		resp.Code, resp.Msg = -1, "invalid DKVS root"
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 || timeout > dkvsMaxWatchSeconds {
		timeout = dkvsMaxWatchSeconds
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(timeout)*time.Second)
	defer cancel()
	root, changed, err := s.model.WaitDKVSDirectory(ctx, req.Prefix, *knownRoot)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		if errors.Is(err, context.Canceled) {
			return
		}
		resp.Code, resp.Msg = -1, err.Error()
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	resp.Data = &dkvsWatchData{Changed: changed, Root: root.String()}
	c.JSON(http.StatusOK, resp)
}
