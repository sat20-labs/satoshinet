package common

import (
	"bytes"
	"fmt"

	"github.com/sat20-labs/satoshinet/txscript"
)

var contractScriptMagic = []byte("CT")

const contractScriptPayloadLen = 22

func ContractPkScript(contract ContractAddress) ([]byte, error) {
	payload := contract.ScriptAddress()
	return txscript.NewScriptBuilder().
		AddOp(txscript.OP_FALSE).
		AddOp(txscript.OP_IF).
		AddData(contractScriptMagic).
		AddData(payload).
		AddOp(txscript.OP_ENDIF).
		AddOp(txscript.OP_FALSE).
		Script()
}

func ParseContractPkScript(pkScript []byte, prefix string) (ContractAddress, bool, error) {
	if len(pkScript) != 30 {
		return ContractAddress{}, false, nil
	}
	if pkScript[0] != txscript.OP_FALSE ||
		pkScript[1] != txscript.OP_IF ||
		pkScript[2] != byte(len(contractScriptMagic)) ||
		!bytes.Equal(pkScript[3:5], contractScriptMagic) ||
		pkScript[5] != contractScriptPayloadLen ||
		pkScript[28] != txscript.OP_ENDIF ||
		pkScript[29] != txscript.OP_FALSE {
		return ContractAddress{}, false, nil
	}

	payload := pkScript[6:28]
	var hash EVMAddress
	copy(hash[:], payload[2:])
	contract, err := NewContractAddress(prefix, payload[0], payload[1], hash)
	if err != nil {
		return ContractAddress{}, false, fmt.Errorf("invalid contract script payload: %w", err)
	}
	return contract, true, nil
}

func IsContractPkScript(pkScript []byte) bool {
	_, ok, _ := ParseContractPkScript(pkScript, TestnetContractPrefix)
	return ok
}
