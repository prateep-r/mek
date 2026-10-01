package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
)

//go:embed example.yaml
var exampleConfig []byte

// Example returns the commented example config.
func Example() []byte { return exampleConfig }

// Init writes the example config if no config exists yet.
func Init(force bool) (string, error) {
	p := Path()
	if _, err := os.Stat(p); err == nil && !force {
		return p, fmt.Errorf("%s already exists (use --force to overwrite)", p)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return p, err
	}
	return p, os.WriteFile(p, exampleConfig, 0o600)
}
