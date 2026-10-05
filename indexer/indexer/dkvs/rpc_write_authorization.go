package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

// WalletWriteContext is a read-only view of CURRENT binding and endpoint
// positions. It is not a lease, receipt, sequence allocator or mutation log.
// A captured authorization ceases to authorize a mutation as soon as its
// prefix changes, including create/delete/recreate within the same block.
type WalletWriteContext struct {
	EndpointID string             `json:"endpoint_id"`
	Prefixes   []PrefixGeneration `json:"prefixes"`
}

type WalletWriteAuthorization struct {
	Context   WalletWriteContext `json:"context"`
	Signature []byte             `json:"signature"`
}

const WalletWriteHeightTolerance = uint64(2)

func validateWalletWriteHeight(issueHeight, currentHeight uint64) error {
	// Subtraction follows the upper-bound check to avoid uint64 underflow.
	if issueHeight > currentHeight || currentHeight-issueHeight > WalletWriteHeightTolerance {
		return ErrStaleEndpoint
	}
	return nil
}

func WriteMutationPrefixes(mutations []CASMutation) ([]string, error) {
	if err := validateCASMutations(mutations); err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, mutation := range mutations {
		prefix, err := CollectionPathForKey(mutation.Record.Key)
		if err != nil {
			return nil, err
		}
		set[prefix] = struct{}{}
	}
	prefixes := make([]string, 0, len(set))
	for prefix := range set {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	return prefixes, nil
}

// WalletWriteDigest commits the ordered batch, including CAS predicates,
// endpoint and request identity. Current account binding is independently
// checked under the commit lock. Record signatures alone do not sign CAS.
func WalletWriteDigest(mutations []CASMutation, options BatchCASOptions, ctx WalletWriteContext) (chainhash.Hash, error) {
	prefixes, err := WriteMutationPrefixes(mutations)
	if err != nil {
		return chainhash.Hash{}, err
	}
	if ctx.EndpointID == "" || ctx.EndpointID != options.EndpointID || options.RequestID == "" || len(options.RequestID) > 128 || len(ctx.Prefixes) != len(prefixes) {
		return chainhash.Hash{}, ErrInvalidRecord
	}
	for n, prefix := range prefixes {
		if ctx.Prefixes[n].Prefix != prefix {
			return chainhash.Hash{}, ErrInvalidRecord
		}
	}
	var data bytes.Buffer
	data.WriteString("SAT20/DKVS/WalletWrite/2\\x00")
	putBytes := func(value []byte) {
		_ = binary.Write(&data, binary.BigEndian, uint32(len(value)))
		_, _ = data.Write(value)
	}
	putBytes([]byte(options.EndpointID))
	putBytes([]byte(options.RequestID))
	_ = binary.Write(&data, binary.BigEndian, uint32(len(ctx.Prefixes)))
	for _, prefix := range ctx.Prefixes {
		putBytes([]byte(prefix.Prefix))
		_ = binary.Write(&data, binary.BigEndian, prefix.Generation)
	}
	_ = binary.Write(&data, binary.BigEndian, uint32(len(mutations)))
	for _, mutation := range mutations {
		hash := RecordHash(mutation.Record)
		data.Write(hash[:])
		if mutation.Precondition.ExpectAbsent {
			data.WriteByte(1)
		} else {
			data.WriteByte(0)
			data.Write(mutation.Precondition.ExpectedHash[:])
		}
	}
	hash := sha256.Sum256(data.Bytes())
	return chainhash.Hash(hash), nil
}
func CloneWalletWriteAuthorization(auth *WalletWriteAuthorization) *WalletWriteAuthorization {
	if auth == nil {
		return nil
	}
	copyAuth := *auth
	copyAuth.Context.Prefixes = append([]PrefixGeneration(nil), auth.Context.Prefixes...)
	copyAuth.Signature = append([]byte(nil), auth.Signature...)
	return &copyAuth
}

func VerifyWalletWriteAuthorization(mutations []CASMutation, options BatchCASOptions, auth *WalletWriteAuthorization) (string, error) {
	if auth == nil || len(auth.Signature) != schnorr.SignatureSize {
		return "", ErrPermissionDenied
	}
	digest, err := WalletWriteDigest(mutations, options, auth.Context)
	if err != nil {
		return "", err
	}
	var account string
	var signer *btcec.PublicKey
	for _, mutation := range mutations {
		pub, err := RecordSignerPubKey(mutation.Record)
		if err != nil {
			return "", err
		}
		id, err := CanonicalAccountID(pub)
		if err != nil {
			return "", err
		}
		if account != "" && account != id {
			return "", ErrPermissionDenied
		}
		account = id
		signer, err = btcec.ParsePubKey(pub)
		if err != nil {
			return "", ErrInvalidSignature
		}
	}
	signature, err := schnorr.ParseSignature(auth.Signature)
	if err != nil || signer == nil || !signature.Verify(digest[:], signer) {
		return "", ErrInvalidSignature
	}
	return account, nil
}

// Called under the SAME lock as batchCASReady/commit. An old authorization
// can only observe an exact present-state retry; it never authorizes restoring
// absence. No persistent per-request or per-deleted-key state is retained.
func (i *Indexer) walletWriteContextCurrentLocked(ctx WalletWriteContext, mutations []CASMutation, height, now uint64) error {
	for _, prefix := range ctx.Prefixes {
		meta, err := i.ensurePathMetaLocked(prefix.Prefix, height, now)
		if err != nil {
			return err
		}
		if meta.EndpointGeneration != prefix.Generation {
			return ErrStaleGeneration
		}
	}
	for _, mutation := range mutations {
		if err := validateWalletWriteHeight(mutation.Record.IssueHeight, height); err != nil {
			return err
		}
	}
	return nil
}
func (i *Indexer) exactCurrentWalletRetryLocked(mutations []CASMutation, height, now uint64) (bool, error) {
	for _, mutation := range mutations {
		current, err := i.getRaw(mutation.Record.Key)
		if IsTombstone(mutation.Record.Flags) {
			if errors.Is(err, ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return false, err
			}
			return false, nil
		}
		if errors.Is(err, ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !existingRecordActive(i, current, height, now) || RecordHash(current) != RecordHash(mutation.Record) {
			return false, nil
		}
	}
	return true, nil
}
