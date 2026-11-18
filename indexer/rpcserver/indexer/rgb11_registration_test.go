package indexer

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/rgb11/strict_types"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestRGB11TechnicalRegistrationHasOnlyBusinessWriteRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&Service{handle: &Handle{}}).InitRouter(router, "")
	count := 0
	for _, route := range router.Routes() {
		if strings.Contains(route.Path, "/v3/rgb11/contracts/register") {
			if route.Method != "POST" || route.Path != "/v3/rgb11/contracts/register" {
				t.Fatalf("unexpected RGB11 registration route %s %s", route.Method, route.Path)
			}
			count++
		}
	}
	if count != 1 {
		t.Fatalf("RGB11 registration route count = %d, want 1", count)
	}
}

func TestRGB11TechnicalRegistrationRejectsUnsignedOrWrongKey(t *testing.T) {
	for _, record := range []*swire.DKVSRecord{
		nil,
		{Key: "/contract/rgb11/short"},
		{Key: "/contract/rgb11/" + strings.Repeat("a", 64), Seq: 1, Value: []byte{rgb11TechnicalValueVersion, 0}},
		{Key: "/contract/rgb11/" + strings.Repeat("a", 64), Seq: 1, Value: []byte{rgb11TechnicalValueVersion + 1, 0}, PubKey: make([]byte, 33)},
	} {
		if _, err := validateRGB11TechnicalRecord(record); err == nil {
			t.Fatalf("accepted malformed technical record: %+v", record)
		}
	}
	t.Run("v1 public payload reaches signature gate", func(t *testing.T) {
		seal, err := seals.NewGraphBlindSeal(bytes.Repeat([]byte{1}, 32), 0, 17)
		require.NoError(t, err)
		issued, err := issuance.Issue(issuance.Spec{Kind: schemas.NIA, Network: issuance.BitcoinRegtest,
			Ticker: "REG", Name: "Registration fixture", Precision: 0, Timestamp: 1_700_000_001,
			Allocations: []issuance.Allocation{{Seal: seal, Amount: 10}}})
		require.NoError(t, err)
		id, err := consensus.ParseContractID(issued.ContractID)
		require.NoError(t, err)
		pubKey, err := hex.DecodeString(indexercommon.GetCoreNodePubKey())
		require.NoError(t, err)
		record := &swire.DKVSRecord{Version: dkvsindexer.Version, Seq: 1, IssueHeight: 1,
			Key: rgb11TechnicalRegistryPrefix + hex.EncodeToString(id[:]), PubKey: pubKey}
		for _, concealed := range []bool{false, true} {
			value := issued.Container.Value.Clone()
			if concealed {
				genesis := rgb11PublicField(&value, "genesis")
				assignments := rgb11PublicUnwrap(rgb11PublicField(genesis, "assignments"))
				require.Len(t, assignments.Entries, 1)
				typed := rgb11PublicUnwrap(&assignments.Entries[0].Value)
				items := rgb11PublicUnwrap(typed.Inner)
				require.Len(t, items.Items, 1)
				genesisSeal, err := seals.NewBlindSeal(seal.TxID[:], seal.Vout, seal.Blinding)
				require.NoError(t, err)
				secret, err := genesisSeal.Conceal()
				require.NoError(t, err)
				raw := strict_types.Value{Kind: strict_types.ValueBytes, Raw: secret[:]}
				*rgb11PublicField(&items.Items[0], "seal") = strict_types.Value{
					Kind: strict_types.ValueUnion, Name: "concealed", Tag: 1, Inner: &raw}
			}
			armor, err := consignment.EncodeArmor(value)
			require.NoError(t, err)
			container, err := consignment.DecodeArmor(armor)
			require.NoError(t, err)
			require.Equal(t, issued.ContractID, container.ContractID)
			content, err := consignment.EncodeFile(container)
			require.NoError(t, err)
			var payload bytes.Buffer
			payload.WriteByte(rgb11TechnicalValueVersion)
			require.NoError(t, swire.WriteVarBytes(&payload, swire.ProtocolVersion, content))
			record.Value = payload.Bytes()
			_, err = validateRGB11TechnicalRecord(record)
			if concealed {
				// Valid V1 public content must reach the unchanged authority gate;
				// it is never registered without a real CoreNode signature.
				require.ErrorIs(t, err, dkvsindexer.ErrInvalidSignature)
			} else {
				require.ErrorIs(t, err, dkvsindexer.ErrInvalidRecord)
			}
		}
	})
}
