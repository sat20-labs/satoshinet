package epoch

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

const (
	// The reason code for delete epoch member
	DelCode_Disconnect      = 1
	DelCode_NoBlockProduced = 2
)

const (
	DelEpochMemberResult_NotConfirm = uint32(0)
	DelEpochMemberResult_Agree      = uint32(1)
	DelEpochMemberResult_Reject     = uint32(2)
)

// Confirm to delete an epoch member from the current epoch list
type DelEpochMember struct {
	// validator id
	ValidatorId    uint64 // The validator id for confirm epoch member delete
	DelValidatorId uint64 // The validator id to be deleted
	DelCode        uint32 // The reason code for delete epoch member
	EpochIndex     int64  // The epoch index for confirm epoch member delete
	Result         uint32 // The result for confirm, DelEpochMemberResult_NotConfirm, DelEpochMemberResult_Agree, DelEpochMemberResult_Reject
	Token          string // The token for epoch handover, it sign by validator to be confirmed
}

func (he *DelEpochMember) GetDelEpochMemTokenData() []byte {

	// Next epoch Token Data format: "satsnet:delepochmem:validatorid:DelValidatorId:DelCode:EpochIndex:timestamp"
	tokenData := fmt.Sprintf("satsnet:delepochmem:%d:%d:%d:%d:%d", he.ValidatorId, he.DelValidatorId, he.DelCode, he.EpochIndex, he.Result)
	tokenSource := sha256.Sum256([]byte(tokenData))
	//return hex.EncodeToString(tokenSource[:])

	return tokenSource[:]
}

func (he *DelEpochMember) VerifyToken(pubKey []byte) bool {
	signatureBytes, err := base64.StdEncoding.DecodeString(he.Token)
	if err != nil {
		utils.Log.Tracef("[DelEpochMember]VerifyToken: Invalid generator token, ignore it.")
		return false
	}

	tokenData := he.GetDelEpochMemTokenData()

	publicKey, err := secp256k1.ParsePubKey(pubKey[:])

	// 解析签名
	// signature, err := btcec.ParseDERSignature(signatureBytes)
	signature, err := ecdsa.ParseDERSignature(signatureBytes)
	if err != nil {
		utils.Log.Tracef("Failed to parse signature: %v", err)
		return false
	}

	// 使用公钥验证签名
	valid := signature.Verify(tokenData, publicKey)
	if valid {
		utils.Log.Tracef("[DelEpochMember]VerifyToken:Signature is valid.")
		return true
	} else {
		utils.Log.Tracef("[DelEpochMember]VerifyToken:Signature is invalid.")
		return false
	}

}
