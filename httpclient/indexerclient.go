package httpclient

import (
	
	"encoding/json"
	"fmt"

)

const (
	TIME_OUT_DURATION = 60
)


type IndexerClient struct {
	*RESTClient
}

func NewIndexerClient(scheme, host string, proxy string) *IndexerClient {
	// net = "mainnet"  -- btc mainnet
	// net = "testnet"  -- btc testnet4, for indexer, it's "testnet"

	
	http := newHTTPClient()

	client := NewRESTClient(scheme, host, proxy, http)
	return &IndexerClient{client}
}


// btcutil.Tx
func (p *IndexerClient) GetRawTx(tx string) (string, error) {
	path := p.GetUrl("/btc/rawtx/" + tx)
	rsp, err := p.Http.SendGetRequest(path)
	if err != nil {
		//Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	fmt.Printf("%v response: %s", path, string(rsp))

	// Unmarshal the response.
	var result TxResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		err := fmt.Errorf("%s response data format failed: %s", path, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		err := fmt.Errorf("%s response failed: %s", path, result.Msg)
		return "", err
	}

	return result.Data.(string), nil
}

func (p *IndexerClient) GetTxUtxoAssets(utxo string) (*TxOutputInfo, error) {
	path := p.GetUrl("/v3/utxo/info/" + utxo)
	rsp, err := p.Http.SendGetRequest(path)
	if err != nil {
		//Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	fmt.Printf("%v response: %s\n", path, string(rsp))

	// Unmarshal the response.
	var result TxOutputResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		err := fmt.Errorf("%s response data format failed: %s", path, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		err := fmt.Errorf("%s response failed: %s", path, result.Msg)
		return nil, err
	}

	return result.Data, nil
}


func (p *IndexerClient) GetAssetSummaryWithAddress(address string) *AssetSummary {
	url := p.GetUrl("/v2/address/summary/" + address)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil
	}

	log.Infof("%v response: %s", url, string(rsp))

	// Unmarshal the response.
	var result AssetSummaryResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		log.Errorf("Unmarshal failed. %v", err)
		return nil
	}

	if result.Code != 0 {
		log.Errorf("%v response message %s", url, result.Msg)
		return nil
	}

	return result.Data
}