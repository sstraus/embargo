package registry

import (
	"context"
	"fmt"

	"github.com/sstraus/embargo/internal/ecosystem"
)

// Mock is an in-memory RegistryClient for tests. Keyed by ecosystem/name/version.
type Mock struct {
	Data map[string]ecosystem.ReleaseMetadata
	Err  map[string]error
}

// NewMock returns an empty Mock.
func NewMock() *Mock {
	return &Mock{Data: map[string]ecosystem.ReleaseMetadata{}, Err: map[string]error{}}
}

// MockKey builds the lookup key used by Mock.
func MockKey(eco, name, version string) string {
	return eco + "/" + name + "@" + version
}

// Set registers metadata for a dependency.
func (m *Mock) Set(meta ecosystem.ReleaseMetadata) {
	m.Data[MockKey(meta.Ecosystem, meta.Name, meta.Version)] = meta
}

// GetReleaseMetadata implements ecosystem.RegistryClient.
func (m *Mock) GetReleaseMetadata(_ context.Context, dep ecosystem.Dependency) (ecosystem.ReleaseMetadata, error) {
	key := MockKey(dep.Ecosystem, dep.Name, dep.Version)
	if err, ok := m.Err[key]; ok {
		return ecosystem.ReleaseMetadata{}, err
	}
	if meta, ok := m.Data[key]; ok {
		return meta, nil
	}
	return ecosystem.ReleaseMetadata{}, fmt.Errorf("mock: no metadata for %s", key)
}
