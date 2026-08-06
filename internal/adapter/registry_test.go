package adapter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// fakeAdapter is a no-op adapter used to exercise the registry.
type fakeAdapter struct{ name string }

func (f fakeAdapter) Name() string                             { return f.name }
func (f fakeAdapter) Run(context.Context, didebaan.Sink) error { return nil }

func factory(name string) Factory {
	return func(map[string]any) (didebaan.Adapter, error) {
		return fakeAdapter{name: name}, nil
	}
}

// withCleanRegistry swaps in an empty registry for the duration of a test so
// cases do not see adapters registered by other packages (or each other).
func withCleanRegistry(t *testing.T) {
	t.Helper()
	mu.Lock()
	saved := registry
	registry = map[string]Factory{}
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		registry = saved
		mu.Unlock()
	})
}

func TestRegisterAndNew(t *testing.T) {
	withCleanRegistry(t)

	Register("alpha", factory("alpha"))
	ad, err := New("alpha", nil)
	require.NoError(t, err)
	assert.Equal(t, "alpha", ad.Name())
}

func TestNewUnknownAdapter(t *testing.T) {
	withCleanRegistry(t)

	_, err := New("missing", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestNamesSorted(t *testing.T) {
	withCleanRegistry(t)

	Register("gamma", factory("gamma"))
	Register("alpha", factory("alpha"))
	Register("beta", factory("beta"))
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, Names())
}

func TestRegisterDuplicatePanics(t *testing.T) {
	withCleanRegistry(t)

	Register("dup", factory("dup"))
	assert.Panics(t, func() { Register("dup", factory("dup")) })
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	withCleanRegistry(t)

	assert.Panics(t, func() { Register("", factory("x")) })
}
