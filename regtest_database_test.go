package main

import (
	"bytes"
	"fmt"
	"github.com/sat20-labs/satoshinet/database"
	"testing"
)

func TestRegressionDatabaseSurvivesStartupReopen(t *testing.T) {
	oldCfg, oldParams := cfg, activeNetParams
	cfg = &config{RegressionTest: true, DbType: "ffldb", DataDir: t.TempDir()}
	activeNetParams = &regressionNetParams
	t.Cleanup(func() { cfg, activeNetParams = oldCfg, oldParams })
	db, err := loadBlockDB()
	if err != nil {
		t.Fatal(err)
	}
	key, value := []byte("regtest-restart-sentinel"), []byte("preserve-existing-state")
	if err := db.Update(func(tx database.Tx) error { return tx.Metadata().Put(key, value) }); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = loadBlockDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx database.Tx) error {
		if !bytes.Equal(tx.Metadata().Get(key), value) {
			return fmt.Errorf("startup deleted regression database")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
