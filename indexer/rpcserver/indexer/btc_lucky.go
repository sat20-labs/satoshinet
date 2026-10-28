package indexer

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sat20-labs/satoshinet/btcec"
)

func (s *Handle) getBTCLuckyBlocks(c *gin.Context) {
	start, startErr := strconv.Atoi(c.DefaultQuery("start", "0"))
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if startErr != nil || limitErr != nil || start < 0 || limit < 1 || limit > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "start must be nonnegative and limit must be between 1 and 1000"})
		return
	}
	pubkey := strings.ToLower(strings.TrimSpace(c.Query("pubkey")))
	if pubkey != "" {
		raw, err := hex.DecodeString(pubkey)
		if err != nil || len(raw) != 33 {
			c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid compressed miner pubkey"})
			return
		}
		if _, err = btcec.ParsePubKey(raw); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid compressed miner pubkey"})
			return
		}
	}
	result, err := s.model.indexer.GetBTCLuckyBlocks(pubkey, start, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": result})
}
