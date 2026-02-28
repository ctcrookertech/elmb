package main

import (
	"os"
	"strings"

	"github.com/ctcrookertech/elmb/core"
)

func resolveAPIKey() string {
	if key := os.Getenv("ELMB_API_KEY"); key != "" {
		return key
	}
	data, err := os.ReadFile("anthropic.key")
	if err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

func main() {
	args := os.Args[1:]
	limit := ModeBuild

	if len(args) >= 1 && args[0] == "--plain" {
		core.Plain = true
		args = args[1:]
	}

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

	apiKey := resolveAPIKey()

	core.Line(core.Enact, "seed: "+command+" "+strings.Join(commandArgs, " "))

	m := NewMachine(limit, apiKey, command, commandArgs)
	m.Run()
}
