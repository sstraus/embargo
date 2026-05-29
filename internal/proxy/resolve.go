package proxy

import (
	"fmt"
	"os"
	"strings"

	"github.com/sstraus/embargo/internal/pathexec"
)

// EnvRealPrefix is the prefix for per-tool real-binary overrides, e.g.
// EMBARGO_REAL_NPM=/usr/local/bin/npm.
const EnvRealPrefix = "EMBARGO_REAL_"

// EnvActive marks that a real tool is already executing under embargo, used to
// prevent the shim from re-entering the proxy recursively.
const EnvActive = "EMBARGO_ACTIVE"

// ResolveReal finds the real executable for tool, skipping the shim directory
// so we never resolve back to ourselves. An EMBARGO_REAL_<TOOL> environment
// override takes precedence.
func ResolveReal(tool, shimDir string) (string, error) {
	if override := os.Getenv(EnvRealPrefix + strings.ToUpper(tool)); override != "" {
		return override, nil
	}
	if p, ok := pathexec.Look(tool, os.Getenv("PATH"), shimDir); ok {
		return p, nil
	}
	return "", fmt.Errorf("real %q binary not found on PATH (outside %s); set %s%s to override",
		tool, shimDir, EnvRealPrefix, strings.ToUpper(tool))
}
