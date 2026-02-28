//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var dist = "dist"

var skip = map[string]bool{
	"dist":         true,
	"magefiles":    true,
	"node_modules": true,
	"core":         true,
}

// targets discovers cmd_<name>.go files in non-skipped directories.
// Each match produces dist/<name> (or dist/<name>.exe on Windows).
func targets() ([]struct{ pkg, bin string }, error) {
	dirs, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var out []struct{ pkg, bin string }
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") || skip[d.Name()] {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(d.Name(), "cmd_*.go"))
		for _, m := range matches {
			name := filepath.Base(m)
			name = strings.TrimPrefix(name, "cmd_")
			name = strings.TrimSuffix(name, ".go")
			bin := filepath.Join(dist, name)
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			out = append(out, struct{ pkg, bin string }{"./" + d.Name(), bin})
		}
	}
	return out, nil
}

// Build compiles all discovered targets to dist/.
func Build() error {
	ts, err := targets()
	if err != nil {
		return err
	}
	os.MkdirAll(dist, 0o755)
	for _, t := range ts {
		fmt.Println("building", t.bin, "from", t.pkg)
		if err := sh("go", "build", "-o", t.bin, t.pkg); err != nil {
			return err
		}
	}
	return nil
}

// Lint runs staticcheck on all packages.
func Lint() error {
	return sh("staticcheck", "./...")
}

// Vet runs go vet on all packages.
func Vet() error {
	return sh("go", "vet", "./...")
}

// Test runs tests for all packages.
func Test() error {
	return sh("go", "test", "./...")
}

// Check runs vet, lint, and test.
func Check() error {
	if err := Vet(); err != nil {
		return err
	}
	if err := Lint(); err != nil {
		return err
	}
	return Test()
}

// Clean removes build artifacts.
func Clean() error {
	fmt.Println("cleaning")
	return os.RemoveAll(dist)
}

// Tidy runs go mod tidy.
func Tidy() error {
	return sh("go", "mod", "tidy")
}

func sh(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
