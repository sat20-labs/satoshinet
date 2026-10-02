package base

import (
	"errors"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	sncommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"
	"github.com/sat20-labs/satoshinet/txscript"
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
			if err != nil || ctype != sncommon.CONTENT_TYPE_DEPLOYCONTRACT {
				continue
			}
			register, err := b.rgb11RegistrationFromSignedTranscend(tx, data)
			if err != nil {
				// A malformed or unauthenticated naming descriptor never makes
				// an otherwise valid SatoshiNet transaction invalid.
				sncommon.Log.Warningf("ignore RGB11 naming candidate in tx %s: %v", tx.Txid, err)
				continue
			}
			if register == nil {
				continue
			}
			events = append(events, rgb11names.Event{
				TxIndex: uint32(txIndex), EventIndex: eventIndex, TxID: tx.Txid,
				Register: register, Optional: true,
			})
			eventIndex++
		}
	}
	return events, nil
}

func (b *BaseIndexer) rgb11RegistrationFromSignedTranscend(tx *sncommon.Transaction, data []byte) (*rgb11names.Register, error) {
	if tx == nil {
		return nil, rgb11names.ErrInvalid
	}
	deploy, err := sncommon.ParseSignedDeployContractInvoice(data)
	if err != nil {
		return nil, err
	}
	register, err := rgb11names.ParseTranscendRegistration(deploy.ContractPath, deploy.ContractContent)
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
	// ProviderDID is a snapshot selected from DKVS /personal/<account>/primary_did
	// before deployment. Ownership/genesis checks belong to the channel deploy
	// validation path; the naming indexer only consumes the authenticated,
	// dual-signed descriptor deterministically during replay.
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
	if sncommon.VerifyMessage(pubA, message, sigA) && sncommon.VerifyMessage(pubB, message, sigB) {
		return true
	}
	return sncommon.VerifyMessage(pubB, message, sigA) && sncommon.VerifyMessage(pubA, message, sigB)
}
