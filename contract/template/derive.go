package template

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
)

const AddressHashLen = 32

func DeriveContractAddress(prefix string, encodedContract []byte, deployer string, random []byte) (ContractAddress, [AddressHashLen]byte, error) {
	hash, err := DeriveContractHash(encodedContract, deployer, random)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	addr, err := contractcommon.NewContractAddressFromHash(
		prefix,
		contractcommon.AddressVersionV1,
		contractcommon.ContractTypeTemplate,
		hash[:],
	)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	return addr, hash, nil
}

func DeriveContractHash(encodedContract []byte, deployer string, random []byte) ([AddressHashLen]byte, error) {
	if len(encodedContract) == 0 {
		return [AddressHashLen]byte{}, errors.New("template contract content is empty")
	}
	if deployer == "" {
		return [AddressHashLen]byte{}, errors.New("template contract deployer is empty")
	}
	if len(random) == 0 {
		return [AddressHashLen]byte{}, errors.New("template contract random value is empty")
	}

	var buf bytes.Buffer
	writeBytes(&buf, encodedContract)
	writeBytes(&buf, []byte(deployer))
	writeBytes(&buf, random)
	return sha256.Sum256(buf.Bytes()), nil
}

func writeBytes(buf *bytes.Buffer, data []byte) {
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(data)))
	buf.Write(lenBuf[:n])
	buf.Write(data)
}
