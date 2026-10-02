package base

import (
	"errors"
	"fmt"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	sncommon "github.com/sat20-labs/satoshinet/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func (b *BaseIndexer) collectRGB11NamingEventsLocked(block *sncommon.Block) ([]rgb11names.Event, error) {
	if block == nil {
		return nil, rgb11names.ErrInvalid
	}
	events := make([]rgb11names.Event, 0)
	for txIndex, tx := range block.Transactions {
		if txIndex == 0 || tx == nil {
			continue
		}
		eventIndex := uint32(0)
		for _, output := range tx.Outputs {
			if output == nil || output.Address == nil || !sncommon.IsOpReturn(output.Address.PkScript) {
				continue
			}
			ctype, data, err := sncommon.ReadDataFromNullDataScript(output.Address.PkScript)
			if err != nil {
				continue
			}
			switch ctype {
			case sncommon.CONTENT_TYPE_PRIMARYDIDBIND:
				bindEvents, err := b.primaryDIDBindEvents(tx, uint32(txIndex), eventIndex, data)
				if err != nil {
					return nil, err
				}
				events = append(events, bindEvents...)
				eventIndex += uint32(len(bindEvents))
			case sncommon.CONTENT_TYPE_DEPLOYCONTRACT:
				register, err := b.rgb11RegistrationFromSignedTranscend(tx, data)
				if err != nil {
					// Malformed/untrusted OP_RETURN data is not allowed to stall
					// the base chain. Only a fully authenticated channel deploy
					// can become an automatic registration event.
					sncommon.Log.Warningf("ignore RGB11 naming candidate in tx %s: %v", tx.Txid, err)
					continue
				}
				if register == nil {
					continue
				}
				// Refresh the provider ownership revision before applying the
				// registration. If the DID moved after the last bind, the old
				// bind becomes inactive and this optional registration is skipped.
				if ownerEvent, err := b.currentOwnerEventForGenesis(register.GenesisAddress, uint32(txIndex), eventIndex, tx.Txid); err != nil {
					return nil, err
				} else if ownerEvent != nil {
					events = append(events, *ownerEvent)
					eventIndex++
				}
				events = append(events, rgb11names.Event{
					TxIndex: uint32(txIndex), EventIndex: eventIndex, TxID: tx.Txid,
					Register: register, Optional: true,
				})
				eventIndex++
			}
		}
	}
	return events, nil
}

func (b *BaseIndexer) primaryDIDBindEvents(tx *sncommon.Transaction, txIndex, eventIndex uint32, data []byte) ([]rgb11names.Event, error) {
	parsed, err := rgb11names.ParsePrimaryDIDBindPayload(data, b.chaincfgParam)
	if err != nil {
		sncommon.Log.Warningf("ignore invalid Primary DID bind in tx %s: %v", tx.Txid, err)
		return nil, nil
	}
	if b.rgb11DIDResolver == nil {
		return nil, fmt.Errorf("%w: Primary DID resolver is unavailable", rgb11names.ErrUnavailable)
	}
	identity, err := b.rgb11DIDResolver.ResolveName(parsed.DID)
	if err != nil {
		return nil, fmt.Errorf("resolve Primary DID %s: %w", parsed.DID, err)
	}
	ownership, ok := verifiedOwnership(parsed.DID, parsed.Address, identity)
	if !ok {
		sncommon.Log.Warningf("ignore unauthorized Primary DID bind %s -> %s", parsed.DID, parsed.Address)
		return nil, nil
	}
	return []rgb11names.Event{
		{TxIndex: txIndex, EventIndex: eventIndex, TxID: tx.Txid, Ownership: ownership},
		{TxIndex: txIndex, EventIndex: eventIndex + 1, TxID: tx.Txid, Bind: &rgb11names.Bind{DID: parsed.DID, Address: parsed.Address}},
	}, nil
}

func verifiedOwnership(did, address string, identity dkvsindexer.DIDIdentity) (*rgb11names.Ownership, bool) {
	if !identity.Active || identity.CanonicalName != did || identity.OwnerUtxo == "" || identity.OwnerSat < 0 {
		return nil, false
	}
	outpoint, err := wire.NewOutPointFromString(identity.OwnerUtxo)
	if err != nil || outpoint.String() != identity.OwnerUtxo {
		return nil, false
	}
	owned := false
	for _, candidate := range identity.OwnerAddresses {
		if strings.EqualFold(strings.TrimSpace(candidate), address) {
			owned = true
			break
		}
	}
	if !owned {
		return nil, false
	}
	return &rgb11names.Ownership{
		DID: did, Address: address, OwnerUtxo: identity.OwnerUtxo,
		OwnerSat: identity.OwnerSat, InscriptionID: identity.InscriptionID,
	}, true
}

func (b *BaseIndexer) currentOwnerEventForGenesis(genesisAddress string, txIndex, eventIndex uint32, txid string) (*rgb11names.Event, error) {
	if b.rgb11Names == nil {
		return nil, rgb11names.ErrUnavailable
	}
	primary, err := b.rgb11Names.Lookup(rgb11names.Query{Kind: "primary", Value: genesisAddress})
	if err != nil {
		if errors.Is(err, rgb11names.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if primary.Binding == nil {
		return nil, nil
	}
	if b.rgb11DIDResolver == nil {
		return nil, fmt.Errorf("%w: Primary DID resolver is unavailable", rgb11names.ErrUnavailable)
	}
	identity, err := b.rgb11DIDResolver.ResolveName(primary.Binding.DID)
	if err != nil {
		return nil, fmt.Errorf("refresh Primary DID %s: %w", primary.Binding.DID, err)
	}
	ownership, ok := verifiedOwnership(primary.Binding.DID, genesisAddress, identity)
	if !ok {
		// Explicitly move the current owner snapshot away from this address if
		// the resolver gives a different active owner. The registration then
		// observes the old bind as inactive.
		if identity.Active && identity.OwnerUtxo != "" && len(identity.OwnerAddresses) == 1 {
			other := strings.TrimSpace(identity.OwnerAddresses[0])
			if other != "" {
				if refreshed, valid := verifiedOwnership(primary.Binding.DID, other, identity); valid {
					return &rgb11names.Event{TxIndex: txIndex, EventIndex: eventIndex, TxID: txid, Ownership: refreshed}, nil
				}
			}
		}
		return nil, nil
	}
	return &rgb11names.Event{TxIndex: txIndex, EventIndex: eventIndex, TxID: txid, Ownership: ownership}, nil
}

func (b *BaseIndexer) rgb11RegistrationFromSignedTranscend(tx *sncommon.Transaction, data []byte) (*rgb11names.Register, error) {
	if tx == nil {
		return nil, rgb11names.ErrInvalid
	}
	deploy, err := sncommon.ParseSignedDeployContractInvoice(data)
	if err != nil {
		return nil, err
	}
	register, err := rgb11names.ParseTranscendRegistration(deploy.ContractPath, deploy.ContractContent, b.chaincfgParam)
	if errors.Is(err, rgb11names.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	channel := b.channelForDeployTx(tx)
	if channel == nil {
		return nil, fmt.Errorf("RGB11 transcend deploy is not funded to a known channel")
	}
	unsigned, err := txscript.NewScriptBuilder().
		AddData([]byte(deploy.ContractPath)).
		AddData(deploy.ContractContent).
		AddInt64(deploy.DeployTime).
		Script()
	if err != nil {
		return nil, err
	}
	if !verifyChannelDeploySignatures(channel, unsigned, deploy.LocalSign, deploy.RemoteSign) {
		return nil, fmt.Errorf("RGB11 transcend deploy channel signatures are invalid")
	}
	return register, nil
}

func (b *BaseIndexer) channelForDeployTx(tx *sncommon.Transaction) *sncommon.ChannelInfo {
	var found *sncommon.ChannelInfo
	for _, output := range tx.Outputs {
		if output == nil || output.Address == nil || sncommon.IsOpReturn(output.Address.PkScript) {
			continue
		}
		for _, address := range output.Address.Addresses {
			channel := b.channelMap[address]
			if channel == nil {
				continue
			}
			if found != nil && found != channel {
				return nil
			}
			found = channel
		}
	}
	return found
}

func verifyChannelDeploySignatures(channel *sncommon.ChannelInfo, message, localSig, remoteSig []byte) bool {
	if channel == nil || len(localSig) == 0 || len(remoteSig) == 0 {
		return false
	}
	pubA, errA := secp256k1.ParsePubKey(channel.PubA)
	pubB, errB := secp256k1.ParsePubKey(channel.PubB)
	sigA, errLA := ecdsa.ParseDERSignature(localSig)
	sigB, errRB := ecdsa.ParseDERSignature(remoteSig)
	if errA != nil || errB != nil || errLA != nil || errRB != nil {
		return false
	}
	direct := sncommon.VerifyMessage(pubA, message, sigA) && sncommon.VerifyMessage(pubB, message, sigB)
	if direct {
		return true
	}
	return sncommon.VerifyMessage(pubB, message, sigA) && sncommon.VerifyMessage(pubA, message, sigB)
}
