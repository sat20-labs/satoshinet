package indexer

import (
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type rgb11DKVSReader interface {
	GetRGB11RegistrationByContract(contractID string) (*dkvsindexer.RGB11Registration, error)
	GetRGB11RegistrationByName(assetName string) (*dkvsindexer.RGB11Registration, error)
	GetRGB11RegistryCount(providerDID, ticker string) (uint64, error)
}

type rgb11DKVSWriter interface {
	PutDKVSInternalRGB11Registry(record *wire.DKVSRecord) (bool, error)
	IsCoreNode(pubkey string) bool
}

type rgb11RegisterRequest struct {
	Record *wire.DKVSRecord `json:"record"`
}

func (s *Service) initRGB11NamingRoutes(r *gin.Engine, proxy string) {
	r.POST(proxy+"/v3/rgb11/register", dkvsLocalOnly, func(c *gin.Context) {
		s.handleRGB11InternalRegister(c)
	})
	r.GET(proxy+"/v3/rgb11/naming/status", func(c *gin.Context) {
		if s == nil || s.handle == nil || s.handle.model == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": -1, "msg": "RGB11 DKVS registry unavailable"})
			return
		}
		if _, ok := s.handle.model.indexer.(rgb11DKVSReader); !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": -1, "msg": "RGB11 DKVS registry unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{"source": "dkvs"}})
	})
	r.GET(proxy+"/v3/rgb11/contract/:contractid", func(c *gin.Context) {
		reader, ok := s.rgb11DKVSReader()
		if !ok {
			rgb11RegistryError(c, dkvsindexer.ErrRecordNotFound, true)
			return
		}
		registration, err := reader.GetRGB11RegistrationByContract(c.Param("contractid"))
		if err != nil {
			rgb11RegistryError(c, err, false)
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": registration})
	})
	r.GET(proxy+"/v3/rgb11/name/:assetname", func(c *gin.Context) {
		reader, ok := s.rgb11DKVSReader()
		if !ok {
			rgb11RegistryError(c, dkvsindexer.ErrRecordNotFound, true)
			return
		}
		registration, err := reader.GetRGB11RegistrationByName(c.Param("assetname"))
		if err != nil {
			rgb11RegistryError(c, err, false)
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": registration})
	})
	r.GET(proxy+"/v3/rgb11/ordinal", func(c *gin.Context) {
		reader, ok := s.rgb11DKVSReader()
		if !ok {
			rgb11RegistryError(c, dkvsindexer.ErrRecordNotFound, true)
			return
		}
		count, err := reader.GetRGB11RegistryCount(c.Query("provider"), c.Query("ticker"))
		if err != nil {
			rgb11RegistryError(c, err, false)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "msg": "ok",
			"data": gin.H{"provider_did": c.Query("provider"), "base_ticker": c.Query("ticker"), "max_ordinal": count},
		})
	})
}

func (s *Service) handleRGB11InternalRegister(c *gin.Context) {
	if s == nil || s.handle == nil || s.handle.model == nil || s.handle.model.indexer == nil {
		rgb11RegistryError(c, dkvsindexer.ErrRecordNotFound, true)
		return
	}
	writer, ok := s.handle.model.indexer.(rgb11DKVSWriter)
	if !ok {
		rgb11RegistryError(c, dkvsindexer.ErrRecordNotFound, true)
		return
	}
	var req rgb11RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Record == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid RGB11 registry request"})
		return
	}
	parsed, err := dkvsindexer.ParseKey(req.Record.Key)
	if err != nil || parsed.Namespace != dkvsindexer.RGB11RegistryNamespace {
		rgb11RegistryError(c, dkvsindexer.ErrInvalidKey, false)
		return
	}
	if len(req.Record.PubKey) == 0 || !writer.IsCoreNode(hex.EncodeToString(req.Record.PubKey)) {
		c.JSON(http.StatusForbidden, gin.H{"code": -1, "msg": "RGB11 registry writer is not a CoreNode"})
		return
	}
	updated, err := writer.PutDKVSInternalRGB11Registry(req.Record)
	if err != nil {
		rgb11RegistryError(c, err, false)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{"updated": updated}})
}

func (s *Service) rgb11DKVSReader() (rgb11DKVSReader, bool) {
	if s == nil || s.handle == nil || s.handle.model == nil {
		return nil, false
	}
	reader, ok := s.handle.model.indexer.(rgb11DKVSReader)
	return reader, ok
}

func rgb11RegistryError(c *gin.Context, err error, unavailable bool) {
	status := http.StatusInternalServerError
	switch {
	case unavailable:
		status = http.StatusServiceUnavailable
	case errors.Is(err, dkvsindexer.ErrRecordNotFound):
		status = http.StatusNotFound
	case errors.Is(err, dkvsindexer.ErrInvalidRecord), errors.Is(err, dkvsindexer.ErrInvalidKey):
		status = http.StatusBadRequest
	}
	c.JSON(status, gin.H{"code": -1, "msg": err.Error()})
}
