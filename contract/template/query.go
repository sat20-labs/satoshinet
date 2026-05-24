package template

type ContractInfo struct {
	Address       string               `json:"address"`
	TemplateName  string               `json:"templateName"`
	Version       uint32               `json:"version"`
	UpdatedHeight int64                `json:"updatedHeight"`
	RuntimeState  TemplateRuntimeState `json:"runtimeState"`
}

type IndexSnapshot struct {
	Height    int                        `json:"height"`
	Runtime   []byte                     `json:"runtime"`
	Contracts map[string]*ContractInfo   `json:"contracts"`
	History   map[string][]HistoryRecord `json:"history"`
}
