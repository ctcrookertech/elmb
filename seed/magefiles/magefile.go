//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

var dist = filepath.Join("..", "dist")
var bin = filepath.Join(dist, "elmb")

func init() {
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
}

// Build compiles the binary.
func Build() error {
	os.MkdirAll(dist, 0o755)
	fmt.Println("building", bin)
	return sh("go", "build", "-o", bin, ".")
}

// Lint runs staticcheck.
func Lint() error {
	return sh("staticcheck", "./...")
}

// Vet runs go vet.
func Vet() error {
	return sh("go", "vet", "./...")
}

// Test runs tests.
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
