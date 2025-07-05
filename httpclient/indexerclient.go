package httpclient

import (
	
	"encoding/json"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
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
	var result indexerwire.TxResp
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

func (p *IndexerClient) GetTxUtxoAssets(utxo string) (*indexer.AssetsInUtxo, error) {
	path := p.GetUrl("/v3/utxo/info/" + utxo)
	rsp, err := p.Http.SendGetRequest(path)
	if err != nil {
		//Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	fmt.Printf("%v response: %s\n", path, string(rsp))

	// Unmarshal the response.
	var result indexerwire.TxOutputRespV3
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

func (p *IndexerClient) GetIndexerPubkey(localPubkey string) (string, error) {
	path := p.GetUrl("/kv/register")
	req := indexerwire.RegisterPubKeyReq{
		PubKey: localPubkey,
	}
	buff, err := json.Marshal(&req)
	if err != nil {
		return "", err
	}
	
	rsp, err := p.Http.SendPostRequest(path, buff)
	if err != nil {
		//Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "",  err
	}

	fmt.Printf("%v response: %s\n", path, string(rsp))

	// Unmarshal the response.
	var result indexerwire.RegisterPubKeyResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		err := fmt.Errorf("%s response data format failed: %s", path, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		err := fmt.Errorf("%s response failed: %s", path, result.Msg)
		return "", err
	}

	return result.PubKey, nil
}
