package indexer

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

// Keep the original state and analytics sources, bounded to the visible page.
// Histories, participants and the analytics' full metrics never enter this view.
func (s *Handle) getPredictionMarket(c *gin.Context) {
	start, limit := parseStartLimit(c)
	if start < 0 || limit < 1 || limit > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid market page"})
		return
	}
	kind := int(contractcommon.ContractTypeAgent)
	query := contractListQuery{
		start: start, limit: limit, contractTypeID: &kind,
		subtype: contractcommon.SubtypePrediction, sortBy: "created_height", desc: true,
	}
	contracts, _ := s.model.GetContracts(0, 0)
	contracts = filterContractSummaries(contracts, query)
	contracts = filterRuntimeExistingContractSummaries(contracts, query)
	sortContractSummaries(contracts, query)
	rows := make([]gin.H, 0)
	for _, summary := range paginateContractSummaries(contracts, start, limit) {
		row := gin.H{"summary": summary}
		state, err := contractStateCall("getcontractstate", []interface{}{summary.Address})
		if err == nil && json.Valid(state) {
			row["state"] = json.RawMessage(state)
		}
		value, err := s.model.GetContractAnalytics(summary.Address)
		analytics, ok := value.(*contractcommon.ContractAnalytics)
		if err == nil && ok && analytics != nil {
			row["analytics"] = gin.H{
				"outcomes":    analytics.Metrics["outcomes"],
				"outcomeBets": analytics.OutcomeBets, "outcomeId": analytics.OutcomeID,
			}
		} else if err != nil {
			row["analytics"] = gin.H{"code": -1, "msg": err.Error()}
		}
		rows = append(rows, row)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": rows, "total": len(contracts)})
}
