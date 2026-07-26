package configgen

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed defaults/override.yaml
var defaultOverrideYAML []byte

// DefaultOverrideYAML returns the embedded override template.
func DefaultOverrideYAML() []byte {
	out := make([]byte, len(defaultOverrideYAML))
	copy(out, defaultOverrideYAML)
	return out
}

// EnsureOverride writes ui/override.yaml from the embedded template if missing.
// Existing files are never overwritten.
func EnsureOverride(overridePath string) error {
	if overridePath == "" {
		return fmt.Errorf("override path empty")
	}
	if _, err := os.Stat(overridePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(overridePath), 0o755); err != nil {
		return err
	}
	tmp := overridePath + ".tmp"
	if err := os.WriteFile(tmp, DefaultOverrideYAML(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, overridePath)
}
