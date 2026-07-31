package p2p

import "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

func (s *handlerTestStore) GetDKVSPathSnapshot(string) (*dkvs.PathSnapshot, error) {
	return nil, dkvs.ErrRecordNotFound
}

func (s *handlerTestStore) ApplyDKVSPathSnapshot(*dkvs.PathSnapshot) (int, error) {
	return 0, nil
}
