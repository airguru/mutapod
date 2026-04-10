package provider

import (
	"fmt"

	"github.com/mutapod/mutapod/internal/config"
	"github.com/mutapod/mutapod/internal/shell"
)

// Factory creates a Provider from a config and a commander.
type Factory func(cfg *config.Config, cmd shell.Commander) (Provider, error)

var registry = map[string]Factory{}

// Register registers a provider factory under the given type name.
// Called from each provider package's init().
func Register(typeName string, f Factory) {
	registry[typeName] = f
}

// New creates the provider described by cfg.Provider.Type.
func New(cfg *config.Config, cmd shell.Commander) (Provider, error) {
	f, ok := registry[cfg.Provider.Type]
	if !ok {
		return nil, fmt.Errorf("provider: unknown type %q", cfg.Provider.Type)
	}
	return f(cfg, cmd)
}
