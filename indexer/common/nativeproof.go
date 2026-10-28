package common

import (
	"bytes"
	"crypto/sha256"
	"fmt"

	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

// SetNativeAssetProof installs the complete proof in the anchor witness and
// its SHA256 commitment in a zero-valued metadata output. Call this BEFORE
// constructing or signing any invoice; native invoices always bind outputs.
// Existing witnesses or commitments are rejected rather than overwritten.
func SetNativeAssetProof(tx *wire.MsgTx, proof []byte) error {
	if err := CheckAnchorTxEncoding(tx); err != nil {
		return err
	}
	if _, err := nativeproof.Decode(proof); err != nil {
		return err
	}
	if len(tx.TxIn[0].Witness) != 0 {
		return fmt.Errorf("anchor already contains a witness")
	}
	// The fifth anchor field is the invoice signature. Empty placeholders
	// and the four-field unsigned invoice are allowed; signed invoices are not.
	tokenizer := txscript.MakeScriptTokenizer(0, tx.TxIn[0].SignatureScript)
	for field := 0; field < 5 && tokenizer.Next(); field++ {
		if field == 4 && len(tokenizer.Data()) != 0 {
			return fmt.Errorf("native proof must be installed before signing the anchor invoice")
		}
	}
	if _, present, err := NativeAssetProof(tx); err != nil || present {
		return fmt.Errorf("anchor already contains a native proof commitment")
	}
	digest := sha256.Sum256(proof)
	script, err := NullDataScript(CONTENT_TYPE_NATIVE_ASSET, digest[:])
	if err != nil {
		return err
	}
	tx.TxIn[0].Witness = wire.TxWitness{append([]byte(nil), proof...)}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return nil
}

// NativeAssetProof checks the unique canonical digest output and exact proof
// witness. Absence of BOTH leaves historical anchors unchanged. Presence of
// only one, a duplicate, malformed framing or a digest mismatch fails closed.
func NativeAssetProof(tx *wire.MsgTx) ([]byte, bool, error) {
	if tx == nil {
		return nil, false, fmt.Errorf("nil anchor transaction")
	}
	var marker *wire.TxOut
	var digest []byte
	for _, output := range tx.TxOut {
		if output == nil {
			return nil, false, fmt.Errorf("nil anchor output")
		}
		kind, data, err := ReadDataFromNullDataScript(output.PkScript)
		if kind != CONTENT_TYPE_NATIVE_ASSET || err != nil {
			// Recognize a truncated native marker as malformed, too. The
			// generic STP parser returns zero when its data push is missing.
			t := txscript.MakeScriptTokenizer(0, output.PkScript)
			if !t.Next() || t.Opcode() != txscript.OP_RETURN || !t.Next() || t.Opcode() != SAT20_MAGIC_NUMBER ||
				!t.Next() || len(t.Data()) > 8 || t.ExtractInt64() != CONTENT_TYPE_NATIVE_ASSET {
				continue
			}
			return nil, true, fmt.Errorf("malformed native proof commitment")
		}
		if marker != nil {
			return nil, true, fmt.Errorf("duplicate native proof commitment")
		}
		canonical, buildErr := NullDataScript(CONTENT_TYPE_NATIVE_ASSET, data)
		if buildErr != nil || len(data) != sha256.Size || !bytes.Equal(canonical, output.PkScript) ||
			output.Value != 0 || len(output.Assets) != 0 {
			return nil, true, fmt.Errorf("noncanonical native proof commitment")
		}
		marker, digest = output, data
	}
	var found bool
	for _, input := range tx.TxIn {
		if input == nil {
			return nil, false, fmt.Errorf("nil anchor input")
		}
		for _, item := range input.Witness {
			found = found || nativeproof.IsProof(item)
		}
	}
	if marker == nil && !found {
		return nil, false, nil
	}
	if marker == nil || len(tx.TxIn) != 1 || len(tx.TxIn[0].Witness) != 1 {
		return nil, true, fmt.Errorf("native proof requires one committed witness item")
	}
	proof := tx.TxIn[0].Witness[0]
	if _, err := nativeproof.Decode(proof); err != nil {
		return nil, true, err
	}
	actual := sha256.Sum256(proof)
	if !bytes.Equal(actual[:], digest) {
		return nil, true, fmt.Errorf("native proof digest mismatch")
	}
	return append([]byte(nil), proof...), true, nil
}
