package wire

import (
	"bytes"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
)

func TestDeserializeTxAssetsRejectsInvalidPrecision(t *testing.T) {
	var encoded bytes.Buffer
	buf := make([]byte, 8)
	if err := WriteVarIntBuf(&encoded, 0, 1, buf); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"brc20", "f", "ooxx", "1:64"} {
		if err := WriteVarBytesBuf(&encoded, 0, []byte(value), buf); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteVarIntBuf(&encoded, 0, 0, buf); err != nil {
		t.Fatal(err)
	}
	var assets TxAssets
	if err := DeserializeTxAssets(&assets, encoded.Bytes()); err == nil {
		t.Fatal("expected invalid asset precision to be rejected")
	}
}

func TestSerializeTxAssetsRejectsInvalidPrecision(t *testing.T) {
	invalid := scommon.NewDefaultDecimal(1)
	invalid.Precision = scommon.MAX_PRECISION + 1
	name := NewAssetNameFromString("brc20:f:ooxx")
	assets := TxAssets{{Name: *name, Amount: *invalid}}
	if _, err := SerializeTxAssets(&assets); err == nil {
		t.Fatal("expected working precision asset to be rejected")
	}
}

func FuzzDeserializeTxAssetsNeverPanics(f *testing.F) {
	f.Add([]byte{0})
	f.Add([]byte{1, 0, 0, 0, 4, '1', ':', '6', '4', 0})
	f.Fuzz(func(t *testing.T, encoded []byte) {
		var assets TxAssets
		_ = DeserializeTxAssets(&assets, encoded)
	})
}
