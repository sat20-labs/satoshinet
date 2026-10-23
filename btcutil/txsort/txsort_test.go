// Copyright (c) 2015-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package txsort_test

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil/txsort"
	"github.com/sat20-labs/satoshinet/wire"
)

// TestSort ensures the transaction sorting works according to the BIP.
// Its original Bitcoin vectors are encoded with a zero SatoshiNet asset count
// in each output so the transactions retain their BIP69 ordering cases.
func TestSort(t *testing.T) {
	tests := []struct {
		name         string
		hexFile      string
		isSorted     bool
		unsortedHash string
		sortedHash   string
	}{
		{
			name:         "first test case from BIP 69 - sorts inputs only, based on hash",
			hexFile:      "bip69-1.hex",
			isSorted:     false,
			unsortedHash: "1a99a8382e4c758acadf2faa18eb2e3f2313b18689d738550a9eb7d4409f75f0",
			sortedHash:   "649d05dc078873f094528dbcfcd5153bf95731e1f0d1885ec018a4acbdd114ad",
		},
		{
			name:         "second test case from BIP 69 - already sorted",
			hexFile:      "bip69-2.hex",
			isSorted:     true,
			unsortedHash: "0b12c217ef7ad16f16208fa2e117366be52aca3a024c64dace68736055b2b9b7",
			sortedHash:   "0b12c217ef7ad16f16208fa2e117366be52aca3a024c64dace68736055b2b9b7",
		},
		{
			name:         "block 100001 tx[1] - sorts outputs only, based on amount",
			hexFile:      "bip69-3.hex",
			isSorted:     false,
			unsortedHash: "237c574c8f78791f601ce58fd9af53be02d049662a5f7d50f005dcb3c86e4a68",
			sortedHash:   "14258606c8182b7ad86e3664bbea9ccf83f70360783bf97a08ae5ccebb786eab",
		},
		{
			name:         "block 100001 tx[2] - sorts both inputs and outputs",
			hexFile:      "bip69-4.hex",
			isSorted:     false,
			unsortedHash: "d82a75177d9ed368c3d3ee921c2b29be6fe7ae729659214c211bf66866379116",
			sortedHash:   "d606dabbabd8d87e79af23d81d978217c7d42d66c6aca5654bab0a49085be702",
		},
		{
			name:         "block 100998 tx[6] - sorts outputs only, based on output script",
			hexFile:      "bip69-5.hex",
			isSorted:     false,
			unsortedHash: "120ea9f47dbdf8d4f354be24a8f9a70089c3e426b923226750aae08be88f9035",
			sortedHash:   "d71a54131c3c96d72defc40ccfaeb6ed011a3e7df1013aa13c667ddc8c61cf0d",
		},
	}

	for _, test := range tests {
		// Load and deserialize the test transaction.
		filePath := filepath.Join("testdata", test.hexFile)
		txHexBytes, err := os.ReadFile(filePath)
		if err != nil {
			t.Errorf("ReadFile (%s): failed to read test file: %v",
				test.name, err)
			continue
		}
		txBytes, err := hex.DecodeString(string(txHexBytes))
		if err != nil {
			t.Errorf("DecodeString (%s): failed to decode tx: %v",
				test.name, err)
			continue
		}
		var tx wire.MsgTx
		err = tx.Deserialize(bytes.NewReader(txBytes))
		if err != nil {
			t.Errorf("Deserialize (%s): unexpected error %v",
				test.name, err)
			continue
		}

		// Ensure the sort order of the original transaction matches the
		// expected value.
		if got := txsort.IsSorted(&tx); got != test.isSorted {
			t.Errorf("IsSorted (%s): sort does not match "+
				"expected - got %v, want %v", test.name, got,
				test.isSorted)
			continue
		}

		// Sort the transaction and ensure the resulting hash is the
		// expected value.
		sortedTx := txsort.Sort(&tx)
		if got := sortedTx.TxHash().String(); got != test.sortedHash {
			t.Errorf("Sort (%s): sorted hash does not match "+
				"expected - got %v, want %v", test.name, got,
				test.sortedHash)
			continue
		}

		// Ensure the original transaction is not modified.
		if got := tx.TxHash().String(); got != test.unsortedHash {
			t.Errorf("Sort (%s): unsorted hash does not match "+
				"expected - got %v, want %v", test.name, got,
				test.unsortedHash)
			continue
		}

		// Now sort the transaction using the mutable version and ensure
		// the resulting hash is the expected value.
		txsort.InPlaceSort(&tx)
		if got := tx.TxHash().String(); got != test.sortedHash {
			t.Errorf("SortMutate (%s): sorted hash does not match "+
				"expected - got %v, want %v", test.name, got,
				test.sortedHash)
			continue
		}
	}
}
