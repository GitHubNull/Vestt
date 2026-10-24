package services

import (
	"fmt"
	"sort"
	"sync"

	"vestt/common/httpclient"
	"vestt/common/indexer"
)

// Deps are the dependencies handed to every indexer factory.
type Deps struct {
	Client *httpclient.Client
	Saver  indexer.ConfigSaver
	Cache  indexer.ResultCache
}

// Factory instantiates one indexer (SPEC-04 §3.3 registry.go).
type Factory func(deps Deps) (indexer.Indexer, error)

// DynamicFactory instantiates a family of indexers (Cardigann engine, one
// per YAML definition — wired in SPEC-05).
type DynamicFactory func(deps Deps) ([]indexer.Indexer, error)

var (
	registryMu       sync.Mutex
	factories        = map[string]Factory{}
	cardigannFactory DynamicFactory // single engine slot (SPEC-05)
)

// Register adds one manual-indexer factory. Duplicate ids panic (programmer
// error at startup).
func Register(id string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := factories[id]; exists {
		panic(fmt.Sprintf("indexer factory registered twice: %s", id))
	}
	factories[id] = factory
}

// RegisterCardigann installs the YAML engine factory (SPEC-05). Calling it
// twice panics.
func RegisterCardigann(factory DynamicFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if cardigannFactory != nil {
		panic("cardigann factory registered twice")
	}
	cardigannFactory = factory
}

// RegisteredFactories returns a sorted copy of the static factories.
func RegisteredFactories() []struct {
	ID      string
	Factory Factory
} {
	registryMu.Lock()
	defer registryMu.Unlock()
	ids := make([]string, 0, len(factories))
	for id := range factories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]struct {
		ID      string
		Factory Factory
	}, 0, len(ids))
	for _, id := range ids {
		out = append(out, struct {
			ID      string
			Factory Factory
		}{ID: id, Factory: factories[id]})
	}
	return out
}

// CardigannFactory returns the engine factory (nil until SPEC-05).
func CardigannFactory() DynamicFactory {
	registryMu.Lock()
	defer registryMu.Unlock()
	return cardigannFactory
}
