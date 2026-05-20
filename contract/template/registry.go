package template

import "fmt"

type Factory func() Runtime

type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func (r *Registry) Register(templateName string, factory Factory) error {
	if templateName == "" {
		return fmt.Errorf("template name is empty")
	}
	if factory == nil {
		return fmt.Errorf("template factory is nil")
	}
	if _, ok := r.factories[templateName]; ok {
		return fmt.Errorf("template %s already registered", templateName)
	}
	r.factories[templateName] = factory
	return nil
}

func (r *Registry) NewRuntime(templateName string) (Runtime, error) {
	factory, ok := r.factories[templateName]
	if !ok {
		return nil, fmt.Errorf("unknown template %s", templateName)
	}
	return factory(), nil
}
