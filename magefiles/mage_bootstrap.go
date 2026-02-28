// Zero-install mage bootstrap.
// Run: go run magefiles/mage_bootstrap.go <target>

//go:build ignore

package main

import (
	"os"

	"github.com/magefile/mage/mage"
)

func main() {
	os.Exit(mage.Main())
}
