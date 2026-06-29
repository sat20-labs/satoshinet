package framework

import contract "github.com/sat20-labs/satoshinet/contract"

type EngineState interface {
	Root() [32]byte
	Snapshot() any
}

type RuntimeStore = EngineState

type RootEngineState struct {
	StateRoot     [32]byte
	StateSnapshot any
}

func (s RootEngineState) Root() [32]byte {
	return s.StateRoot
}

func (s RootEngineState) Snapshot() any {
	return s.StateSnapshot
}

type StateSet struct {
	Engines map[ModuleType]EngineState
	Roots   map[ModuleType][32]byte
}

func NewStateSet() *StateSet {
	return &StateSet{
		Engines: make(map[ModuleType]EngineState),
		Roots:   make(map[ModuleType][32]byte),
	}
}

func (s *StateSet) SetRoot(module ModuleType, root [32]byte) {
	if s.Roots == nil {
		s.Roots = make(map[ModuleType][32]byte)
	}
	s.Roots[module] = root
}

func (s *StateSet) SetEngine(module ModuleType, engine EngineState) {
	if s.Engines == nil {
		s.Engines = make(map[ModuleType]EngineState)
	}
	s.Engines[module] = engine
	if engine != nil {
		s.SetRoot(module, engine.Root())
	}
}

func (s *StateSet) Root(module ModuleType) [32]byte {
	if s == nil {
		return [32]byte{}
	}
	if s.Roots != nil {
		if root, ok := s.Roots[module]; ok {
			return root
		}
	}
	if s.Engines != nil && s.Engines[module] != nil {
		return s.Engines[module].Root()
	}
	return [32]byte{}
}

func (s *StateSet) CombinedRoot() [32]byte {
	if s == nil {
		return contract.CombineStateRoots([32]byte{}, [32]byte{}, [32]byte{})
	}
	return contract.CombineStateRoots(
		s.Root(ModuleTemplate),
		s.Root(ModuleEVM),
		s.Root(ModuleAgent),
	)
}

func StateSetFromRoots(roots map[ModuleType][32]byte) *StateSet {
	state := NewStateSet()
	for module, root := range roots {
		state.SetRoot(module, root)
	}
	return state
}
