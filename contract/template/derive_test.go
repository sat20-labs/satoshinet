package template

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

func TestDeriveContractAddressUsesTemplateTypeAndHash32(t *testing.T) {
	encoded := []byte{0x01, 0x02, 0x03}
	random := []byte("random")

	addr, hash, err := DeriveContractAddress(contractcommon.TestnetContractPrefix, encoded, "deployer", random)
	require.NoError(t, err)
	require.Equal(t, contractcommon.ContractTypeTemplate, addr.ContractType())
	require.Equal(t, hash[:], contractcommon.ContractAddressHashBytes(addr))
	require.Len(t, contractcommon.ContractAddressHashBytes(addr), AddressHashLen)
}

func TestDeriveContractAddressRandomChangesAddress(t *testing.T) {
	encoded := []byte{0x01, 0x02, 0x03}

	addr1, _, err := DeriveContractAddress(contractcommon.TestnetContractPrefix, encoded, "deployer", []byte("random-1"))
	require.NoError(t, err)
	addr2, _, err := DeriveContractAddress(contractcommon.TestnetContractPrefix, encoded, "deployer", []byte("random-2"))
	require.NoError(t, err)
	require.False(t, addr1.Equal(addr2))
}
