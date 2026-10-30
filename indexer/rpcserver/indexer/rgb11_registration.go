package indexer

import (
	"bytes"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/strict_types"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const rgb11TechnicalRegistryPrefix = "/contract/rgb11/"
const rgb11TechnicalValueVersion = byte(2)

type rgb11RegistrationRequest struct {
	Record *swire.DKVSRecord `json:"record"`
}

type rgb11RegistrationResponse struct {
	indexerwire.BaseResp
	ErrorCode  string `json:"error_code,omitempty"`
	ContractID string `json:"contract_id,omitempty"`
}

// The indexer service has no wallet signing key. A CoreNode signs the public
// technical record, then this business route independently checks the RGB
// contract before the authority DKVS store sees it. Generic wallet CAS cannot
// write contract keys.
func (s *Handle) registerRGB11Contract(c *gin.Context) {
	resp := &rgb11RegistrationResponse{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}}
	var req rgb11RegistrationRequest
	if err := bindDKVSJSON(c, &req, dkvsRecordHTTPBodyLimit); err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	id, err := validateRGB11TechnicalRecord(req.Record)
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	// An existing identical public value is idempotent. A different value for
	// the same full ContractID is a conflict, never a replacement.
	existing, err := s.model.indexer.GetDKVSRecord(req.Record.Key)
	if err == nil {
		if _, err = validateRGB11TechnicalRecord(existing); err == nil {
			if bytes.Equal(existing.Value, req.Record.Value) {
				resp.ContractID = id
				c.JSON(http.StatusOK, resp)
				return
			}
			err = dkvsindexer.ErrWriteConflict
		}
	} else if errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		_, err = s.model.indexer.PutDKVSInternalContract(req.Record)
		// A concurrent submitter may have installed the same immutable public
		// contract after our initial read. Verify that record before treating the
		// operation as an idempotent success.
		if errors.Is(err, dkvsindexer.ErrWriteConflict) {
			stored, readErr := s.model.indexer.GetDKVSRecord(req.Record.Key)
			if readErr == nil {
				_, readErr = validateRGB11TechnicalRecord(stored)
			}
			if readErr == nil && bytes.Equal(stored.Value, req.Record.Value) {
				err = nil
			}
		}
	}
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	// A successful write is only reported after authoritative readback. The
	// caller can query by full ContractID after an uncertain HTTP outcome.
	stored, err := s.model.indexer.GetDKVSRecord(req.Record.Key)
	if err == nil {
		_, err = validateRGB11TechnicalRecord(stored)
	}
	if err == nil && !bytes.Equal(stored.Value, req.Record.Value) {
		err = dkvsindexer.ErrWriteConflict
	}
	if err != nil {
		setDKVSError(&resp.BaseResp, &resp.ErrorCode, err)
		c.JSON(dkvsHTTPStatus(err), resp)
		return
	}
	resp.ContractID = id
	c.JSON(http.StatusOK, resp)
}

func rgb11PublicUnwrap(v *strict_types.Value) *strict_types.Value {
	for v != nil && v.Kind == strict_types.ValueTuple && len(v.Items) == 1 {
		v = &v.Items[0]
	}
	return v
}

func rgb11PublicField(v *strict_types.Value, name string) *strict_types.Value {
	v = rgb11PublicUnwrap(v)
	if v == nil || v.Kind != strict_types.ValueStruct {
		return nil
	}
	for i := range v.Fields {
		if v.Fields[i].Name == name {
			return &v.Fields[i].Value
		}
	}
	return nil
}

// Public contract data has no transfer history, terminal disclosures or
// revealed genesis seals. The validator checks the on-wire v2 business value
// itself rather than trusting a client's "public" flag or a display name.
func validateRGB11TechnicalRecord(record *swire.DKVSRecord) (string, error) {
	invalid := dkvsindexer.ErrInvalidRecord
	if record == nil || !strings.HasPrefix(record.Key, rgb11TechnicalRegistryPrefix) ||
		record.Seq != 1 || record.TTL != 0 || record.Flags != 0 || len(record.FeeProof) != 0 ||
		len(record.PubKey) != 33 || hex.EncodeToString(record.PubKey) != indexercommon.GetCoreNodePubKey() ||
		len(record.Value) < 2 || record.Value[0] != rgb11TechnicalValueVersion {
		return "", invalid
	}
	id := strings.TrimPrefix(record.Key, rgb11TechnicalRegistryPrefix)
	rawID, err := hex.DecodeString(id)
	if err != nil || len(rawID) != 32 || id != strings.ToLower(id) || strings.Trim(id, "0") == "" {
		return "", invalid
	}
	reader := bytes.NewReader(record.Value[1:])
	content, err := swire.ReadVarBytes(reader, swire.ProtocolVersion, swire.MaxDKVSBlobValueSize, "RGB11 contract")
	if err != nil || reader.Len() != 0 {
		return "", invalid
	}
	container, err := consignment.DecodeFile(content)
	if err != nil || container.Armor == nil || container.Armor.Type != "contract" || !container.GenesisValid {
		return "", invalid
	}
	canonical, err := consignment.EncodeFile(container)
	if err != nil || !bytes.Equal(canonical, content) {
		return "", invalid
	}
	parsedID, err := consensus.ParseContractID(container.ContractID)
	if err != nil || hex.EncodeToString(parsedID[:]) != id {
		return "", invalid
	}
	for _, field := range container.Value.Fields {
		switch field.Name {
		case "version", "transfer", "schema", "types", "scripts", "genesis":
		case "terminals":
			v := rgb11PublicUnwrap(&field.Value)
			if v == nil || v.Kind != strict_types.ValueMap || len(v.Entries) != 0 {
				return "", invalid
			}
		case "bundles":
			v := rgb11PublicUnwrap(&field.Value)
			if v == nil || v.Kind != strict_types.ValueList || len(v.Items) != 0 {
				return "", invalid
			}
		default:
			return "", invalid
		}
	}
	genesis := rgb11PublicField(&container.Value, "genesis")
	assignments := rgb11PublicUnwrap(rgb11PublicField(genesis, "assignments"))
	if assignments == nil || assignments.Kind != strict_types.ValueMap {
		return "", invalid
	}
	for _, entry := range assignments.Entries {
		state := rgb11PublicUnwrap(&entry.Value)
		if state == nil || state.Kind != strict_types.ValueUnion || state.Inner == nil {
			return "", invalid
		}
		items := rgb11PublicUnwrap(state.Inner)
		if items == nil || items.Kind != strict_types.ValueList {
			return "", invalid
		}
		for _, item := range items.Items {
			allocation := rgb11PublicUnwrap(&item)
			if allocation == nil || allocation.Kind != strict_types.ValueUnion ||
				allocation.Name != "confidentialSeal" || allocation.Inner == nil {
				return "", invalid
			}
		}
	}
	schema, schemaOK := container.Value.Field("schema")
	types, typesOK := container.Value.Field("types")
	genesisValue, genesisOK := container.Value.Field("genesis")
	if !schemaOK || !typesOK || !genesisOK {
		return "", invalid
	}
	metadata, err := schemas.ExtractGenesisAssetMetadata(schema, types, genesisValue)
	if err != nil || strings.TrimSpace(metadata.Ticker) == "" ||
		strings.ContainsAny(metadata.Ticker, "@:") {
		return "", invalid
	}
	if _, err := schemas.ByKind(container.GenesisReport.Kind); err != nil {
		return "", invalid
	}
	if err := dkvsindexer.VerifyRecordForClient(record,
		dkvsindexer.RecordVerificationOptions{ExpectedKey: record.Key}); err != nil {
		return "", err
	}
	return id, nil
}
