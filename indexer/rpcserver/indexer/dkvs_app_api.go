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
	Record       *swire.DKVSRecord `json:"record"`
	ExpectedHash string            `json:"expected_hash,omitempty"`
	ExpectAbsent bool              `json:"expect_absent,omitempty"`
}

type dkvsBatchCASReq struct {
	Mutations []dkvsCASMutationReq `json:"mutations"`
}

type dkvsBatchCASData struct {
	Applied int                 `json:"applied"`
	Records []*swire.DKVSRecord `json:"records"`
	Hashes  []string            `json:"hashes"`
}

type dkvsBatchCASResp struct {
	indexerwire.BaseResp
	Data *dkvsBatchCASData `json:"data,omitempty"`
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

func (s *Handle) putDKVSRecordCAS(c *gin.Context) {
	resp := &dkvsRecordResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsCASMutationReq
	if err := bindDKVSJSON(c, &req, dkvsRecordHTTPBodyLimit); err != nil {
		resp.Code, resp.Msg = -1, err.Error()
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	condition, err := parseDKVSPrecondition(req.ExpectedHash, req.ExpectAbsent)
	if err == nil && req.Record == nil {
		err = dkvsindexer.ErrInvalidRecord
	}
	if err == nil {
		_, err = s.model.PutDKVSRecordCAS(req.Record, condition)
	}
	if err != nil {
		resp.Code, resp.Msg = -1, err.Error()
		status := http.StatusBadRequest
		if errors.Is(err, dkvsindexer.ErrWriteConflict) {
			status = http.StatusConflict
		}
		c.JSON(status, resp)
		return
	}
	resp.Data = req.Record
	resp.Hash = dkvsindexer.RecordHash(req.Record).String()
	c.JSON(http.StatusOK, resp)
}

func (s *Handle) putDKVSRecordBatchCAS(c *gin.Context) {
	resp := &dkvsBatchCASResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req dkvsBatchCASReq
	if err := bindDKVSJSON(c, &req, dkvsBatchHTTPBodyLimit); err != nil {
		resp.Code, resp.Msg = -1, err.Error()
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	mutations := make([]dkvsindexer.CASMutation, 0, len(req.Mutations))
	for _, mutation := range req.Mutations {
		condition, err := parseDKVSPrecondition(mutation.ExpectedHash, mutation.ExpectAbsent)
		if err != nil || mutation.Record == nil {
			resp.Code, resp.Msg = -1, dkvsindexer.ErrInvalidRecord.Error()
			c.JSON(http.StatusBadRequest, resp)
			return
		}
		mutations = append(mutations, dkvsindexer.CASMutation{Record: mutation.Record, Precondition: condition})
	}
	applied, err := s.model.PutDKVSRecordBatchCAS(mutations)
	if err != nil {
		resp.Code, resp.Msg = -1, err.Error()
		status := http.StatusBadRequest
		if errors.Is(err, dkvsindexer.ErrWriteConflict) {
			status = http.StatusConflict
		}
		c.JSON(status, resp)
		return
	}
	data := &dkvsBatchCASData{Applied: applied, Records: make([]*swire.DKVSRecord, 0, len(mutations)), Hashes: make([]string, 0, len(mutations))}
	for _, mutation := range mutations {
		data.Records = append(data.Records, mutation.Record)
		data.Hashes = append(data.Hashes, dkvsindexer.RecordHash(mutation.Record).String())
	}
	resp.Data = data
	c.JSON(http.StatusOK, resp)
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
