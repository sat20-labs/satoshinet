package framework

type StateViewContext struct {
	Height          int64
	Timestamp       int64
	ContractAddress string
	AssetReader     StateViewAssetReader
}

type StateViewAssetReader interface {
	AssetBalance(contractAddress, assetName string) (string, error)
}

type StateViewProvider interface {
	StateView(ctx StateViewContext) (interface{}, error)
}
