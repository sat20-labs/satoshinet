package framework

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
)

type TransferOutputRequest struct {
	To                    string
	AssetName             string
	AssetAmt              string
	SatValue              int64
	SatoshiAssetName      string
	Reason                string
	MaxPrecision          int
	InvalidAsset          error
	RequireIntegerSatoshi bool
	IntegerSatoshiError   string
}

func ResultOutputFromTransferFields(req TransferOutputRequest) (ResultOutput, error) {
	if req.AssetName == req.SatoshiAssetName {
		value := req.SatValue
		if req.AssetAmt != "" {
			amt, err := parseTransferAmount(req.AssetAmt, req.MaxPrecision,
				req.RequireIntegerSatoshi, req.IntegerSatoshiError)
			if err != nil {
				return ResultOutput{}, err
			}
			value += amt.Int64()
		}
		return ResultOutput{
			To:        req.To,
			Value:     value,
			AssetName: req.AssetName,
			AssetAmt:  req.AssetAmt,
			Reason:    req.Reason,
		}, nil
	}
	assets, err := NewAssetSetWithPrecision(req.AssetName, req.AssetAmt, req.MaxPrecision, req.InvalidAsset)
	if err != nil {
		return ResultOutput{}, err
	}
	return ResultOutput{
		To:        req.To,
		Value:     req.SatValue,
		AssetName: req.AssetName,
		AssetAmt:  req.AssetAmt,
		Reason:    req.Reason,
		Assets:    assets,
	}, nil
}

type TransferIntentRequest struct {
	From                  contract.ContractAddress
	To                    string
	AssetName             string
	AssetAmt              string
	SatValue              int64
	SatoshiAssetName      string
	MaxPrecision          int
	IntentIndex           uint32
	RequireIntegerSatoshi bool
	IntegerSatoshiError   string
}

func AssetIntentsFromTransferFields(req TransferIntentRequest) ([]AssetIntent, error) {
	out := make([]AssetIntent, 0, 2)
	if req.AssetName != "" && req.AssetAmt != "" {
		amount, err := parseTransferAmount(req.AssetAmt, req.MaxPrecision,
			req.RequireIntegerSatoshi && req.AssetName == req.SatoshiAssetName,
			req.IntegerSatoshiError)
		if err != nil {
			return nil, err
		}
		if amount.Sign() > 0 {
			out = append(out, AssetIntent{
				IntentIndex: req.IntentIndex,
				From:        req.From,
				To:          req.To,
				AssetName:   req.AssetName,
				Amount:      amount,
			})
		}
	}
	if req.SatValue > 0 {
		out = append(out, AssetIntent{
			IntentIndex: req.IntentIndex,
			From:        req.From,
			To:          req.To,
			AssetName:   req.SatoshiAssetName,
			Amount:      scommon.NewDefaultDecimal(req.SatValue),
		})
	}
	return out, nil
}

func parseTransferAmount(amount string, maxPrecision int, requireInteger bool, integerError string) (*scommon.Decimal, error) {
	parsed, err := scommon.NewDecimalFromString(amount, maxPrecision)
	if err != nil {
		return nil, err
	}
	if !requireInteger || parsed.Precision == 0 {
		return parsed, nil
	}
	normalized := parsed.NewPrecision(0)
	if normalized.NewPrecision(parsed.Precision).Cmp(parsed) != 0 {
		if integerError != "" {
			return nil, fmt.Errorf("%s", integerError)
		}
		return nil, fmt.Errorf("satoshi amount must be integer")
	}
	return normalized, nil
}
