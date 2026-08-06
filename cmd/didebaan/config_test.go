package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parse builds the parser the way main() does and parses args against a fresh
// CLI, returning it for assertions.
func parse(t *testing.T, cfgPath string, args ...string) *CLI {
	t.Helper()
	var cli CLI
	parser, err := kong.New(&cli, kongOptions(cfgPath)...)
	require.NoError(t, err)
	_, err = parser.Parse(args)
	require.NoError(t, err)
	return &cli
}

// writeConfig writes a temp YAML config and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// The three layering tests assert file < env < flag on the otlp-endpoint flag.

func TestConfigFileValue(t *testing.T) {
	cfg := writeConfig(t, "otlp-endpoint: from-file:4317\n")
	cli := parse(t, cfg, "collect")
	assert.Equal(t, "from-file:4317", cli.OTLPEndpoint)
}

func TestEnvOverridesFile(t *testing.T) {
	cfg := writeConfig(t, "otlp-endpoint: from-file:4317\n")
	t.Setenv("DIDEBAAN_OTLP_ENDPOINT", "from-env:4317")
	cli := parse(t, cfg, "collect")
	assert.Equal(t, "from-env:4317", cli.OTLPEndpoint)
}

func TestFlagOverridesEnv(t *testing.T) {
	cfg := writeConfig(t, "otlp-endpoint: from-file:4317\n")
	t.Setenv("DIDEBAAN_OTLP_ENDPOINT", "from-env:4317")
	cli := parse(t, cfg, "--otlp-endpoint", "from-flag:4317", "collect")
	assert.Equal(t, "from-flag:4317", cli.OTLPEndpoint)
}

func TestAdapterDefault(t *testing.T) {
	// No file, no env, no flag: the built-in default applies.
	cli := parse(t, "", "collect")
	assert.Equal(t, "claude-code", cli.Adapter)
}

func TestResolveConfigPathFlagOverridesEnv(t *testing.T) {
	t.Setenv("DIDEBAAN_CONFIG", "/from/env.yaml")
	assert.Equal(t, "/from/flag.yaml", resolveConfigPath([]string{"--config", "/from/flag.yaml"}))
	assert.Equal(t, "/from/env.yaml", resolveConfigPath([]string{"collect"}))
}
