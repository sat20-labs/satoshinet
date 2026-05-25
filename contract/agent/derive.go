package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
)

const AddressHashLen = 32

func DeriveContractAddress(prefix string, subtype string, content []byte, deployer string, random []byte) (ContractAddress, [AddressHashLen]byte, error) {
	hash, err := DeriveContractHash(subtype, content, deployer, random)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	addr, err := contractcommon.NewContractAddressFromHash(
		prefix,
		contractcommon.AddressVersionV1,
		contractcommon.ContractTypeAgent,
		hash[:],
	)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	return addr, hash, nil
}

func DeriveContractHash(subtype string, content []byte, deployer string, random []byte) ([AddressHashLen]byte, error) {
	if subtype == "" {
		return [AddressHashLen]byte{}, errors.New("agent contract subtype is empty")
	}
	if len(content) == 0 {
		return [AddressHashLen]byte{}, errors.New("agent contract content is empty")
	}
	if deployer == "" {
		return [AddressHashLen]byte{}, errors.New("agent contract deployer is empty")
	}
	if len(random) == 0 {
		return [AddressHashLen]byte{}, errors.New("agent contract random value is empty")
	}

	var buf bytes.Buffer
	writeHashBytes(&buf, []byte(subtype))
	writeHashBytes(&buf, content)
	writeHashBytes(&buf, []byte(deployer))
	writeHashBytes(&buf, random)
	return sha256.Sum256(buf.Bytes()), nil
}

func writeHashBytes(buf *bytes.Buffer, data []byte) {
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(data)))
	buf.Write(lenBuf[:n])
	buf.Write(data)
}
