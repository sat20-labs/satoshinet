package indexer

import (
	"net/http"

	"github.com/gin-gonic/gin"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type internalContractWriter interface {
	PutDKVSInternalContract(*wire.DKVSRecord) (bool, error)
}

func (s *Service) initInternalContractRoutes(r *gin.Engine, proxy string) {
	r.POST(proxy+"/v3/dkvs/internal/contract", dkvsLocalOnly, func(c *gin.Context) {
		if s == nil || s.handle == nil || s.handle.model == nil || s.handle.model.indexer == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": -1, "msg": "contract store unavailable"})
			return
		}
		writer, ok := s.handle.model.indexer.(internalContractWriter)
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": -1, "msg": "contract store unavailable"})
			return
		}
		var req struct {
			Record *wire.DKVSRecord `json:"record"`
		}
		if err := bindDKVSJSON(c, &req, dkvsBatchHTTPBodyLimit); err != nil || req.Record == nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid contract record"})
			return
		}
		parsed, err := dkvsindexer.ParseKey(req.Record.Key)
		if err != nil || !dkvsindexer.IsAuthorityContractKey(parsed) {
			c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": dkvsindexer.ErrInvalidKey.Error(), "error_code": dkvsindexer.ErrorCodeOf(dkvsindexer.ErrInvalidKey)})
			return
		}
		updated, err := writer.PutDKVSInternalContract(req.Record)
		if err != nil {
			c.JSON(dkvsHTTPStatus(err), gin.H{"code": -1, "msg": err.Error(), "error_code": dkvsindexer.ErrorCodeOf(err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{"updated": updated}})
	})
}
