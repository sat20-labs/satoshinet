package contract

type ContractAnalytics struct {
	Address        string                 `json:"address"`
	ContractType   string                 `json:"contractType,omitempty"`
	ContractTypeID byte                   `json:"contractTypeId,omitempty"`
	Subtype        string                 `json:"subtype,omitempty"`
	Name           string                 `json:"name,omitempty"`
	Version        uint32                 `json:"version,omitempty"`
	Status         string                 `json:"status,omitempty"`
	UpdatedHeight  int64                  `json:"updatedHeight,omitempty"`
	Metrics        map[string]interface{} `json:"metrics,omitempty"`
	Details        map[string]interface{} `json:"details,omitempty"`
	TotalBets      int                    `json:"totalBets,omitempty"`
	TotalAmount    string                 `json:"totalAmount,omitempty"`
	OutcomeBets    map[string]int         `json:"outcomeBets,omitempty"`
	Confirmations  int                    `json:"confirmations,omitempty"`
	Rejections     int                    `json:"rejections,omitempty"`
	ResultType     string                 `json:"resultType,omitempty"`
	OutcomeID      string                 `json:"outcomeId,omitempty"`
}

type ContractUserStatus struct {
	Address  string                  `json:"address"`
	Contract string                  `json:"contract"`
	Metrics  map[string]interface{}  `json:"metrics,omitempty"`
	History  []ContractHistoryRecord `json:"history,omitempty"`
	Details  map[string]interface{}  `json:"details,omitempty"`
}

type ContractOutcomeView struct {
	ID      string `json:"id"`
	Text    string `json:"text,omitempty"`
	Count   int    `json:"count"`
	Amount  string `json:"amount,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

type ContractTransferView struct {
	Address   string `json:"address"`
	AssetName string `json:"assetName,omitempty"`
	Amount    string `json:"amount,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type QueryService interface {
	SupportedContracts() []string
	DeployedContracts(start, limit int) ([]string, int)
	Contracts(start, limit int) ([]ContractSummary, int)
	Contract(contractAddress string) (ContractSummary, error)
	History(contractAddress string, start, limit int) ([]ContractHistoryRecord, int, error)
	Analytics(contractAddress string) (*ContractAnalytics, error)
	InvokeItemByInUtxo(contractAddress, inUtxo string) (*ContractHistoryRecord, error)
	AllAddresses(contractAddress string, start, limit int) ([]string, int, error)
	UserStatus(contractAddress, address string) (*ContractUserStatus, error)
	HistoryByAddress(contractAddress, address string, start, limit int) ([]ContractHistoryRecord, int, error)
}
