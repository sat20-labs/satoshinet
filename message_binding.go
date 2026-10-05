package main

import (
	"strings"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type serverMessageBindingResolver struct {
	server *server
}

func newServerMessageBindingResolver(s *server) *serverMessageBindingResolver {
	r := &serverMessageBindingResolver{server: s}
	r.installDKVSWalletAdmission()
	return r
}

func (r *serverMessageBindingResolver) current(accountID string) (*wire.DKVSRecord, string, error) {
	if r == nil || r.server == nil || r.server.assetIndexer == nil || !validateAccountID(accountID) {
		return nil, "", ErrMessageBindingNotFound
	}
	pubKey, err := dkvs.AccountPubKey(accountID)
	if err != nil {
		return nil, "", ErrMessageBindingNotFound
	}
	params := r.server.assetIndexer.GetChainParam()
	address, err := dkvs.P2TRAddressFromPubKeyBytes(pubKey, params)
	if err != nil || params == nil {
		return nil, "", ErrMessageBindingNotFound
	}
	key, err := dkvs.AccountMappingKey(params.Name, address)
	if err != nil {
		return nil, "", ErrMessageBindingNotFound
	}
	record, err := r.server.assetIndexer.GetDKVSRecord(key)
	if err != nil || record == nil {
		return nil, "", ErrMessageBindingNotFound
	}
	_, _, descriptor, err := dkvs.ValidateAccountMappingBindingRecord(record)
	if err != nil || descriptor.AccountID != strings.ToLower(accountID) {
		return nil, "", ErrMessageBindingNotFound
	}
	return record, descriptor.CoreNodeID, nil
}

func (r *serverMessageBindingResolver) CoreNodeForAccount(accountID string) (string, error) {
	_, coreID, err := r.current(accountID)
	return coreID, err
}

func (r *serverMessageBindingResolver) AccountBoundToCore(accountID, coreNodeID string) bool {
	_, currentCore, err := r.current(accountID)
	return err == nil && currentCore == coreNodeID
}
