package main

import (
	"os"
	"strings"

	"github.com/ctcrookertech/elmb/core"
)

func main() {
	args := os.Args[1:]
	limit := ModeBuild

	if len(args) >= 2 && args[0] == "--limit" {
		limit = parseLimit(args[1])
		args = args[2:]
	}

	if len(args) < 1 {
		core.Errorf("usage: elmb [--limit enact|learn|model|build] <command> [args...]")
		os.Exit(1)
	}

	command := args[0]
	commandArgs := args[1:]

	core.Line(core.Enact, "seed: "+command+" "+strings.Join(commandArgs, " "))

	m := NewMachine(limit, command, commandArgs)
	m.Run()
}
