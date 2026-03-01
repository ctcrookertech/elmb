package main

import (
	"os"
	"strings"

	"github.com/ctcrookertech/elmb/core"
)

func main() {
	args := os.Args[1:]
	limit := ModeBuild
	var valueOverrides string
	var baseFrame string
	var traceFilePath string
	debug := false

	flagsDone := false
	for len(args) > 0 && !flagsDone {
		switch args[0] {
		case "--plain":
			core.Plain = true
			args = args[1:]
		case "--limit":
			if len(args) < 2 {
				core.Errorf("--limit requires a value")
				os.Exit(1)
			}
			limit = parseLimit(args[1])
			args = args[2:]
		case "--value":
			if len(args) < 2 {
				core.Errorf("--value requires a value")
				os.Exit(1)
			}
			valueOverrides = args[1]
			args = args[2:]
		case "--frame":
			if len(args) < 2 {
				core.Errorf("--frame requires a value")
				os.Exit(1)
			}
			baseFrame = args[1]
			args = args[2:]
		case "--trace":
			if len(args) < 2 {
				core.Errorf("--trace requires a file path")
				os.Exit(1)
			}
			traceFilePath = args[1]
			args = args[2:]
		case "--debug":
			debug = true
			args = args[1:]
		default:
			flagsDone = true
		}
	}

	if len(args) < 1 {
		core.Errorf("usage: elmb [--plain] [--limit enact|learn|model|build] [--value overrides] [--frame text] [--trace file] [--debug] <command> [args...]")
		os.Exit(1)
	}

	if traceFilePath != "" {
		f, err := os.Create(traceFilePath)
		if err != nil {
			core.Errorf("cannot open trace file: %v", err)
			os.Exit(1)
		}
		defer f.Close()
		core.TraceFile = f
		Trace.Enabled = true
		core.Line(core.Enact, "tracing to "+traceFilePath)
	}

	command := args[0]
	commandArgs := args[1:]

	config := &Config{
		Values: ParseValueOverrides(valueOverrides),
	}

	if debug {
		Trace.Enabled = true
		Trace.Interactive = true
	}

	core.Line(core.Enact, "seed: "+command+" "+strings.Join(commandArgs, " "))

	m := NewMachine(limit, config, command, commandArgs, baseFrame)
	if err := m.Run(); err != nil {
		os.Exit(1)
	}
}
