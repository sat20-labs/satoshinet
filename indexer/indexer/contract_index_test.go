package indexer

import (
	"encoding/json"
	"testing"

	db "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/stretchr/testify/require"
)

func TestContractIndexBufferPersistsPreparedSnapshot(t *testing.T) {
	kvdb := db.NewKVDB(t.TempDir())
	require.NotNil(t, kvdb)
	defer kvdb.Close()

	mgr := &IndexerMgr{
		baseDB:                  kvdb,
		chaincfgParam:           &chaincfg.TestNetParams,
		templateRuntimeStore:    tmplcontract.NewRuntimeStore(),
		templateContractIndex:   make(map[string]*tmplcontract.ContractInfo),
		templateContractHistory: make(map[string][]tmplcontract.HistoryRecord),
		contractIndex:           make(map[string]*contractengine.ContractSummary),
		contractHistory:         make(map[string][]contractengine.ContractHistoryRecord),
	}
	mgr.templateContractIndex["tc-prepared"] = &tmplcontract.ContractInfo{
		Address:       "tc-prepared",
		TemplateName:  tmplcontract.TemplateAMM,
		UpdatedHeight: 10,
	}
	mgr.contractIndex["tc-prepared"] = &contractengine.ContractSummary{
		Address:       "tc-prepared",
		ContractType:  "template",
		UpdatedHeight: 10,
	}

	mgr.prepareContractIndexBuffer(10)

	mgr.templateContractIndex["tc-live"] = &tmplcontract.ContractInfo{
		Address:       "tc-live",
		TemplateName:  tmplcontract.TemplateLimitOrder,
		UpdatedHeight: 11,
	}
	mgr.contractIndex["tc-live"] = &contractengine.ContractSummary{
		Address:       "tc-live",
		ContractType:  "template",
		UpdatedHeight: 11,
	}

	mgr.persistContractIndexBuffer()

	var contractSnapshot contractIndexSnapshot
	contractEncoded, err := db.GetRawValueFromDB([]byte(contractIndexSnapshotKey), kvdb)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(contractEncoded, &contractSnapshot))
	require.Equal(t, 10, contractSnapshot.Height)
	require.Contains(t, contractSnapshot.Contracts, "tc-prepared")
	require.NotContains(t, contractSnapshot.Contracts, "tc-live")

	var templateSnapshot tmplcontract.IndexSnapshot
	templateEncoded, err := db.GetRawValueFromDB([]byte(templateContractIndexSnapshotKey), kvdb)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(templateEncoded, &templateSnapshot))
	require.Equal(t, 10, templateSnapshot.Height)
	require.Contains(t, templateSnapshot.Contracts, "tc-prepared")
	require.NotContains(t, templateSnapshot.Contracts, "tc-live")
}
