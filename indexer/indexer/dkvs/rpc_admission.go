package dkvs

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

// WalletRPCAdmission is the wallet-facing write boundary. A standalone
// account mapping remains the sole bootstrap exception. Internal writers and
// P2P current-state installation do not use wallet request authorization.
type WalletRPCAdmission struct {
	Indexer        *Indexer
	IsCoreNode     func() bool
	CurrentBinding func(accountID string) (*wire.DKVSRecord, error)
}

type rpcBindingSnapshot struct {
	key  string
	hash chainhash.Hash
}

func rpcBindingDenied(reason string) error {
	return fmt.Errorf("%w: %s", ErrPermissionDenied, reason)
}

// A record signature proves authorship, not permission to submit arbitrary new
// CAS conditions. Business writes additionally require the wallet to sign this
// batch's endpoint and endpoint-local prefix generations. The current binding
// is checked again under the commit lock.
func (a *WalletRPCAdmission) PutRecords(mutations []CASMutation, options BatchCASOptions,
	authorization *WalletWriteAuthorization) (*WriteResult, error) {
	if a == nil || a.Indexer == nil || a.IsCoreNode == nil || a.CurrentBinding == nil || !a.IsCoreNode() {
		return nil, rpcBindingDenied("wallet KV writes require the bound CoreNode")
	}
	mutations = cloneCASMutations(mutations)
	if err := validateCASMutations(mutations); err != nil {
		return nil, err
	}
	if err := a.Indexer.ValidateBatchEndpointID(mutations, options.EndpointID); err != nil {
		return nil, err
	}
	local := strings.ToLower(strings.TrimSpace(a.Indexer.EndpointID()))
	if local == "" {
		return nil, rpcBindingDenied("CoreNode identity is unavailable")
	}
	// Preserve the account-management bootstrap path. It cannot be smuggled
	// into a business batch, and only accepts a descriptor for this CoreNode.
	if len(mutations) == 1 && IsAccountMappingBindingKey(mutations[0].Record.Key) {
		return a.putWalletBinding(mutations[0], options, local)
	}
	bindings := make(map[string]rpcBindingSnapshot)
	for _, mutation := range mutations {
		record := mutation.Record
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return nil, err
		}
		if parsed.Namespace == "account" {
			return nil, rpcBindingDenied("account mapping must be a standalone binding write")
		}
		if err := VerifySignature(record); err != nil {
			return nil, err
		}
		if err := ValidateRecordIdentity(record, parsed); err != nil {
			return nil, err
		}
		pubKey, err := RecordSignerPubKey(record)
		if err != nil {
			return nil, err
		}
		account, err := CanonicalAccountID(pubKey)
		if err != nil {
			return nil, err
		}
		if _, checked := bindings[account]; checked {
			continue
		}
		binding, err := a.CurrentBinding(account)
		if err != nil || binding == nil {
			return nil, rpcBindingDenied("wallet is not bound to this CoreNode")
		}
		_, _, descriptor, err := ValidateAccountMappingBindingRecord(binding)
		if err != nil || descriptor.AccountID != account || strings.ToLower(strings.TrimSpace(descriptor.CoreNodeID)) != local {
			return nil, rpcBindingDenied("wallet is bound to a different CoreNode")
		}
		bindings[account] = rpcBindingSnapshot{key: binding.Key, hash: RecordHash(binding)}
	}
	if len(bindings) != 1 {
		return nil, rpcBindingDenied("one wallet must authorize the entire atomic batch")
	}
	authorization = CloneWalletWriteAuthorization(authorization)
	account, err := VerifyWalletWriteAuthorization(mutations, options, authorization)
	if err != nil {
		return nil, err
	}
	if _, exists := bindings[account]; !exists {
		return nil, rpcBindingDenied("wallet is not bound to this CoreNode")
	}
	return a.Indexer.putBoundWalletBatch(mutations, options, bindings, authorization.Context)
}

func (a *WalletRPCAdmission) putWalletBinding(mutation CASMutation, options BatchCASOptions,
	local string) (*WriteResult, error) {
	record := mutation.Record
	_, _, descriptor, err := ValidateAccountMappingBindingRecord(record)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(strings.TrimSpace(descriptor.CoreNodeID)) != local {
		return nil, rpcBindingDenied("binding target is not this CoreNode")
	}
	return a.Indexer.putBoundWalletBatch([]CASMutation{mutation}, options, nil, WalletWriteContext{})
}

func (i *Indexer) rpcBindingsCurrentLocked(bindings map[string]rpcBindingSnapshot) error {
	for _, binding := range bindings {
		current, err := i.getRaw(binding.key)
		if err != nil || current == nil || RecordHash(current) != binding.hash {
			return rpcBindingDenied("wallet binding changed before KV commit")
		}
	}
	return nil
}

func (i *Indexer) putBoundWalletBatch(mutations []CASMutation, options BatchCASOptions,
	bindings map[string]rpcBindingSnapshot, authorization WalletWriteContext) (*WriteResult, error) {
	for attempt := 0; attempt < 3; attempt++ {
		prep, err := i.prepareBatchCAS(mutations, options)
		if err != nil {
			if errors.Is(err, ErrConcurrentUpdate) {
				continue
			}
			return nil, err
		}
		i.mutex.Lock()
		height, now := i.currentHeight(), currentUnixMilli()
		err = i.rpcBindingsCurrentLocked(bindings)
		var exact bool
		if err == nil {
			exact, err = i.exactCurrentWalletRetryLocked(mutations, height, now)
		}
		// A matching current value or current absence can be reported without
		// executing anything. Every state-changing operation must still match
		// the SIGNED context and the short IssueHeight window at commit time.
		if err == nil && !exact {
			if len(bindings) == 0 {
				// The validated standalone binding is the sole bootstrap
				// exception to request authorization, not to fresh height.
				err = validateWalletWriteHeight(mutations[0].Record.IssueHeight, height)
			} else {
				err = i.walletWriteContextCurrentLocked(authorization, mutations, height, now)
			}
		}
		var ready []preparedCASMutation
		if err == nil {
			ready, err = i.batchCASReadyLocked(prep, height, now)
		}
		if err == nil && len(ready) != 0 {
			err = i.validateBatchStateLocked(ready, height, now)
		}
		var events []batchCASEvent
		var states []PrefixGeneration
		if err == nil && len(ready) != 0 {
			events, states, err = i.commitBatchCASLocked(ready, height, now)
		} else if err == nil {
			states, err = i.prefixStatesForPreparedLocked(prep.mutations)
		}
		i.mutex.Unlock()
		if errors.Is(err, ErrConcurrentUpdate) {
			continue
		}
		if err != nil {
			return nil, err
		}
		i.emitCommittedEvents(events)
		result := &WriteResult{Applied: len(ready), ServerTimeMS: now, ViewHeight: height,
			EndpointID: i.EndpointID(), RequestID: options.RequestID, PrefixStates: states}
		for _, item := range prep.mutations {
			record := cloneRecord(item.mutation.Record)
			result.Records = append(result.Records, record)
			result.Hashes = append(result.Hashes, RecordHash(record).String())
			if i.mutationIsLocalOnly(item) {
				result.LocalOnly = true
			}
		}
		return result, nil
	}
	return nil, ErrConcurrentUpdate
}
