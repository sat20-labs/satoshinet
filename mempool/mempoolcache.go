package mempool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type TxCacheItem struct {
	Txid string `json:"txid"`
	Raw  string `json:"raw"`
}

const (
	cacheName = "mempoolcache.json"
)

func LoadMempoolCahce(dataDir string) ([]*TxCacheItem, error) {

	cachePath := filepath.Join(dataDir, cacheName)

	data, err := loadCacheData(cachePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		err := fmt.Errorf("load local mempool cache failed: %s", err.Error())
		return nil, err

	}

	var items []*TxCacheItem
	err = json.Unmarshal(data, &items)
	if err != nil {
		err := fmt.Errorf("load local mempool cache failed: %s", err.Error())
		return nil, err
	}

	return items, nil
}
func SaveMempoolCahce(dataDir string, items []*TxCacheItem) error {

	cachePath := filepath.Join(dataDir, cacheName)

	data, err := json.Marshal(items)
	if err != nil {
		err := fmt.Errorf("Save local mempool cache failed: %s", err.Error())
		return err
	}
	err = saveCacheData(cachePath, data)
	if err != nil {
		err := fmt.Errorf("save local mempool cache failed: %s", err.Error())
		return err
	}

	return nil
}
func ClearMempoolCahce(dataDir string) error {
	cachePath := filepath.Join(dataDir, cacheName)
	err := os.Remove(cachePath)
	if err != nil {
		log.Errorf("Clear local mempool cache failed: %s", err.Error())
		return err
	}

	log.Info("Clear local mempool cache success")
	return nil
}

func loadCacheData(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func saveCacheData(path string, data []byte) error {
	err := os.WriteFile(path, data, 0600)
	if err != nil {
		return err
	}
	return nil
}
