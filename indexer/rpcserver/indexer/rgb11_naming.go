package indexer

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"
)

// Optional capability: existing Indexer implementations and unrelated RPC
// mocks are not forced to pretend they support the RGB11 naming registry.
type rgb11NamingReader interface {
	GetRGB11Naming(rgb11names.Query) (*rgb11names.Result, error)
}

func (s *Service) initRGB11NamingRoutes(r *gin.Engine, proxy string) {
	r.GET(proxy+"/v3/rgb11/naming/status", func(c *gin.Context) {
		s.handle.queryRGB11Naming(c, rgb11names.Query{Kind: "status"})
	})
	r.GET(proxy+"/v3/rgb11/contract/:contractid", func(c *gin.Context) {
		s.handle.queryRGB11Naming(c, rgb11names.Query{Kind: "contract", Value: c.Param("contractid")})
	})
	r.GET(proxy+"/v3/rgb11/name/:assetname", func(c *gin.Context) {
		s.handle.queryRGB11Naming(c, rgb11names.Query{Kind: "name", Value: c.Param("assetname")})
	})
	r.GET(proxy+"/v3/rgb11/ordinal", func(c *gin.Context) {
		s.handle.queryRGB11Naming(c, rgb11names.Query{Kind: "counter", Provider: c.Query("provider"), Ticker: c.Query("ticker")})
	})
	// Deliberately no POST, PUT, DELETE or ordinal-reservation endpoint.
}

func (h *Handle) queryRGB11Naming(c *gin.Context, query rgb11names.Query) {
	var data *rgb11names.Result
	err := rgb11names.ErrUnavailable
	if h != nil && h.model != nil {
		if reader, ok := h.model.indexer.(rgb11NamingReader); ok {
			data, err = reader.GetRGB11Naming(query)
		}
	}
	if err != nil {
		status := http.StatusServiceUnavailable
		switch {
		case errors.Is(err, rgb11names.ErrInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, rgb11names.ErrNotFound):
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	if data == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": -1, "msg": rgb11names.ErrUnavailable.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": data})
}
