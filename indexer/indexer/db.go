package indexer

import (
	"fmt"
	"path/filepath"

	"github.com/sat20-labs/indexer/common"
	db "github.com/sat20-labs/indexer/indexer/db"
)

func openDB(filepath string) (common.KVDB, error) {
	ldb := db.NewKVDB(filepath)
	if ldb == nil {
		return nil, fmt.Errorf("NewKVDB failed")
	}

	return ldb, nil
}

func (p *IndexerMgr) initDB() (err error) {
	common.Log.Info("InitDB-> start...")

	p.baseDB, err = openDB(p.dbDir + "base")
	if err != nil {
		return err
	}

	p.localDB, err = openDB(p.dbDir + "local")
	if err != nil {
		return err
	}

	// Off-chain records must survive deletion of the rebuildable chain index.
	p.dkvsDB, err = openDB(filepath.Join(p.cfg.DataPath, "db", "dkvs", p.chaincfgParam.Name))
	if err != nil {
		return err
	}

	return nil
}
