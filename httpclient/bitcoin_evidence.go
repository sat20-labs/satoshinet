package httpclient

import (
	"encoding/json"
	"fmt"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

// These helpers consume the existing v3 Bitcoin fact APIs. They deliberately
// do not use indexer asset projections for client-side validated protocols.
func (p *IndexerClient) GetBitcoinRawTx(txid string) (*indexerwire.BitcoinRawTx, error) {
	var response indexerwire.BitcoinRawTxResp
	if err := p.bitcoinEvidencePost("/v3/bitcoin/rawtx/batch", indexerwire.BitcoinTxIDsReq{TxIDs: []string{txid}}, &response); err != nil {
		return nil, err
	}
	if response.Code != 0 || len(response.Data) != 1 || response.Data[0] == nil || response.Data[0].TxID != txid || response.Data[0].Error != "" {
		return nil, fmt.Errorf("invalid Bitcoin raw transaction response")
	}
	return response.Data[0], nil
}

func (p *IndexerClient) GetBitcoinTxStatus(txid string) (*indexerwire.BitcoinTxStatus, error) {
	var response indexerwire.BitcoinTxStatusResp
	if err := p.bitcoinEvidencePost("/v3/bitcoin/tx/status/batch", indexerwire.BitcoinTxIDsReq{TxIDs: []string{txid}}, &response); err != nil {
		return nil, err
	}
	if response.Code != 0 || len(response.Data) != 1 || response.Data[0] == nil || response.Data[0].TxID != txid || response.Data[0].Error != "" {
		return nil, fmt.Errorf("invalid Bitcoin transaction status response")
	}
	return response.Data[0], nil
}

func (p *IndexerClient) GetBitcoinOutspend(outpoint string) (*indexerwire.BitcoinOutspend, error) {
	var response indexerwire.BitcoinOutspendsResp
	if err := p.bitcoinEvidencePost("/v3/bitcoin/outspends/batch", indexerwire.BitcoinOutpointsReq{Outpoints: []string{outpoint}}, &response); err != nil {
		return nil, err
	}
	if response.Code != 0 || len(response.Data) != 1 || response.Data[0] == nil || response.Data[0].Outpoint != outpoint || response.Data[0].Error != "" {
		return nil, fmt.Errorf("invalid Bitcoin outspend response")
	}
	return response.Data[0], nil
}

func (p *IndexerClient) GetBitcoinBlockHeader(height uint32) (*indexerwire.BitcoinBlockHeader, error) {
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return nil, fmt.Errorf("Bitcoin evidence client is missing")
	}
	raw, err := p.Http.SendGetRequest(p.GetUrl(fmt.Sprintf("/v3/bitcoin/block-header/%d", height)))
	if err != nil {
		return nil, err
	}
	var response indexerwire.BitcoinBlockHeaderResp
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Code != 0 || response.Data == nil || response.Data.Height != int64(height) {
		return nil, fmt.Errorf("invalid Bitcoin block header response")
	}
	return response.Data, nil
}

func (p *IndexerClient) bitcoinEvidencePost(path string, request, response any) error {
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return fmt.Errorf("Bitcoin evidence client is missing")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	raw, err := p.Http.SendPostRequest(p.GetUrl(path), body)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, response)
}
