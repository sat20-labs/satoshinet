package localvalidator

import (
	"errors"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/stp"
)

type ValidatorKey struct {
	publicKey  []byte
}

func NewValidatorKey(pubKey []byte, netParams *chaincfg.Params) (*ValidatorKey) {

	vlildatorKey := &ValidatorKey{
		publicKey:  pubKey,
	}

	return vlildatorKey
}

func (v *ValidatorKey) GetPublicKey() ([]byte, error) {

	if v.publicKey == nil {
		return nil, errors.New("invalid public key")
	}

	return v.publicKey, nil
}

func (v *ValidatorKey) SignData(data []byte) ([]byte, error) {

	// 钱包需要先解锁
	// 在钱包解锁时确保stp的wallet跟这里保存的pubkey是一致的

	return stp.SignMsg(data)
}
