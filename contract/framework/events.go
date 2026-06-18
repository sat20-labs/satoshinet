package framework

import contract "github.com/sat20-labs/satoshinet/contract"

type IndexEventKind = contract.IndexEventKind
type IndexEvent = contract.IndexEvent

const (
	IndexEventDeploy    = contract.IndexEventDeploy
	IndexEventInvoke    = contract.IndexEventInvoke
	IndexEventResult    = contract.IndexEventResult
	IndexEventStateRoot = contract.IndexEventStateRoot
)
