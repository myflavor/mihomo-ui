package configgen

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed defaults/base.yaml
var defaultBaseYAML []byte

// DefaultBaseYAML returns the embedded base template.
func DefaultBaseYAML() []byte {
	out := make([]byte, len(defaultBaseYAML))
	copy(out, defaultBaseYAML)
	return out
}

// EnsureBase writes ui/base.yaml from the embedded template if missing.
// Existing files are never overwritten.
func EnsureBase(basePath string) error {
	if basePath == "" {
		return fmt.Errorf("base path empty")
	}
	if _, err := os.Stat(basePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		return err
	}
	tmp := basePath + ".tmp"
	if err := os.WriteFile(tmp, DefaultBaseYAML(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, basePath)
}
