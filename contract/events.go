package contract

type IndexEventKind string

const (
	IndexEventDeploy    IndexEventKind = "deploy"
	IndexEventInvoke    IndexEventKind = "invoke"
	IndexEventResult    IndexEventKind = "result"
	IndexEventStateRoot IndexEventKind = "state_root"
)

type IndexEvent struct {
	Kind           IndexEventKind         `json:"kind"`
	Height         int64                  `json:"height"`
	TxID           string                 `json:"txid,omitempty"`
	Contract       string                 `json:"contract,omitempty"`
	ContractType   string                 `json:"contractType,omitempty"`
	ContractTypeID byte                   `json:"contractTypeId,omitempty"`
	Subtype        string                 `json:"subtype,omitempty"`
	Name           string                 `json:"name,omitempty"`
	Version        uint32                 `json:"version,omitempty"`
	Action         string                 `json:"action,omitempty"`
	Status         string                 `json:"status,omitempty"`
	Actor          string                 `json:"actor,omitempty"`
	GasLimit       int64                  `json:"gasLimit,omitempty"`
	Nonce          uint64                 `json:"nonce,omitempty"`
	Details        map[string]interface{} `json:"details,omitempty"`
}
