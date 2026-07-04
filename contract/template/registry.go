package template

import "fmt"

type Factory func() Contract

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

func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	mustRegister(r, TemplateLimitOrder, func() Contract { return NewLimitOrderContract("") })
	mustRegister(r, TemplateAMM, func() Contract { return NewAMMContract("", "", 0, "") })
	mustRegister(r, TemplateExchange, func() Contract { return NewExchangeContract("", "", "", nil) })
	mustRegister(r, TemplateAutopay, func() Contract { return NewAutopayContract("", "", "", "", "", 0) })
	return r
}

func mustRegister(r *Registry, templateName string, factory Factory) {
	if err := r.Register(templateName, factory); err != nil {
		panic(err)
	}
}

func (r *Registry) NewContract(templateName string) (Contract, error) {
	factory, ok := r.factories[templateName]
	if !ok {
		return nil, fmt.Errorf("unknown template %s", templateName)
	}
	return factory(), nil
}
