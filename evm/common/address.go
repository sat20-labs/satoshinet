package common

import (
	"errors"
	"fmt"
	"strings"

	"github.com/btcsuite/btcutil/bech32"
)

type ContractAddress struct {
	Prefix  string
	Version byte
	Type    byte
	Hash    EVMAddress
}

func NewContractAddress(prefix string, version, typ byte, hash EVMAddress) (ContractAddress, error) {
	if prefix != MainnetContractPrefix && prefix != TestnetContractPrefix {
		return ContractAddress{}, fmt.Errorf("invalid contract address prefix %q", prefix)
	}
	if version != AddressVersionV1 {
		return ContractAddress{}, fmt.Errorf("unsupported contract address version %d", version)
	}
	if typ == 0 {
		return ContractAddress{}, errors.New("contract type must be non-zero")
	}
	return ContractAddress{
		Prefix:  prefix,
		Version: version,
		Type:    typ,
		Hash:    hash,
	}, nil
}

func (a ContractAddress) Encode() (string, error) {
	if a.Prefix == "" {
		return "", errors.New("missing contract address prefix")
	}
	payload := make([]byte, 0, 22)
	payload = append(payload, a.Version, a.Type)
	payload = append(payload, a.Hash[:]...)
	data, err := bech32.ConvertBits(payload, 8, 5, true)
	if err != nil {
		return "", err
	}
	return bech32.Encode(a.Prefix, data)
}

func DecodeContractAddress(s string) (ContractAddress, error) {
	hrp, data, err := bech32.Decode(strings.ToLower(s))
	if err != nil {
		return ContractAddress{}, err
	}
	decoded, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return ContractAddress{}, err
	}
	if len(decoded) != 22 {
		return ContractAddress{}, fmt.Errorf("invalid contract address payload length %d", len(decoded))
	}
	var hash EVMAddress
	copy(hash[:], decoded[2:])
	return NewContractAddress(hrp, decoded[0], decoded[1], hash)
}

func (a ContractAddress) MustEncode() string {
	s, err := a.Encode()
	if err != nil {
		panic(err)
	}
	return s
}

func (a ContractAddress) Equal(b ContractAddress) bool {
	return a.Prefix == b.Prefix && a.Version == b.Version && a.Type == b.Type && a.Hash == b.Hash
}

func (a ContractAddress) Validate() error {
	_, err := NewContractAddress(a.Prefix, a.Version, a.Type, a.Hash)
	return err
}
