package contract_e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func getIndexerJSON(url string, out interface{}) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected indexer status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
