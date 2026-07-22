package dkvs

import (
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	templatecontract "github.com/sat20-labs/satoshinet/contract/template"
)

const (
	DefaultTestNetAutopayDeployer = "tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g"

	DefaultTestNetAutopayDeployNonce    uint64 = 8888
	DefaultAutopayServiceName                  = "dkvs"
	DefaultAutopayMinAmountPerBlock            = "1"
	DefaultAutopayFullRecordFeePerBlock        = "1"
)

// NetworkDefaults contains DKVS policy defaults that are safe to publish in
// code. They do not by themselves authorize writes: fee verification still
// requires a live active autopay contract state.
type NetworkDefaults struct {
	Enabled                  bool
	AutopayDeployer          string
	AutopayRecipient         string
	AutopayFeeAssetName      string
	AutopayServiceName       string
	AutopayMinAmountPerBlock string
	AutopayDeployNonce       uint64
	AutopayContract          string
	FullRecordFeePerBlock    string
	UseAutopayFeeVerifier    bool
}

// NetworkDefaultsForParams returns hard-coded DKVS defaults for known
// networks. Mainnet intentionally has no active default fee verifier until the
// production contract/deployer/recipient are finalized.
func NetworkDefaultsForParams(params *chaincfg.Params) NetworkDefaults {
	if params == nil || params.Name == chaincfg.MainNetParams.Name {
		return NetworkDefaults{}
	}
	defaults := NetworkDefaults{
		Enabled:                  true,
		AutopayDeployer:          DefaultTestNetAutopayDeployer,
		AutopayRecipient:         DefaultTestNetAutopayDeployer,
		AutopayFeeAssetName:      contractcommon.GasAssetNameForNet(params.Net),
		AutopayServiceName:       DefaultAutopayServiceName,
		AutopayMinAmountPerBlock: DefaultAutopayMinAmountPerBlock,
		AutopayDeployNonce:       DefaultTestNetAutopayDeployNonce,
		FullRecordFeePerBlock:    DefaultAutopayFullRecordFeePerBlock,
		UseAutopayFeeVerifier:    true,
	}
	defaults.AutopayContract = defaults.deriveAutopayContract(params)
	return defaults
}

func (d NetworkDefaults) AutopayContent() ([]byte, error) {
	return contractcommon.EncodeTemplateAutopayContent(contractcommon.TemplateAutopayContract{
		ServiceName:       d.AutopayServiceName,
		Recipient:         d.AutopayRecipient,
		FeeAssetName:      d.AutopayFeeAssetName,
		MinAmountPerBlock: d.AutopayMinAmountPerBlock,
	})
}

func (d NetworkDefaults) deriveAutopayContract(params *chaincfg.Params) string {
	if params == nil || d.AutopayDeployer == "" || d.AutopayDeployNonce == 0 {
		return ""
	}
	content, err := d.AutopayContent()
	if err != nil {
		return ""
	}
	addr, _, err := templatecontract.DeriveContractAddress(
		templatecontract.ContractPrefixForNet(params.Net),
		content,
		d.AutopayDeployer,
		d.AutopayDeployNonce,
	)
	if err != nil {
		return ""
	}
	return addr.MustEncode()
}
