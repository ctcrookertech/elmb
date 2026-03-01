package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ctcrookertech/elmb/core"
)

// Trace holds global debug tracing state.
var Trace struct {
	Enabled     bool
	Interactive bool
}

const (
	traceTag      = "\033[90m[   trace]\033[0m"
	debugTag      = "\033[90m[   debug]\033[0m"
	traceTagPlain = "[   trace]"
	debugTagPlain = "[   debug]"
)

func traceFileWrite(format string, args ...any) {
	core.TracePrint(fmt.Sprintf(format, args...))
}

// TraceLine writes a trace message to stderr. No-op when tracing is disabled.
func TraceLine(category, message string) {
	if !Trace.Enabled {
		return
	}
	fmt.Fprintf(os.Stderr, "%s%s %s: %s\n", core.Prefix(), traceTag, category, message)
	traceFileWrite("%s%s %s: %s\n", core.PlainPrefix(), traceTagPlain, category, message)
}

// TraceState dumps machine state summary to stderr.
func TraceState(m *Machine) {
	if !Trace.Enabled {
		return
	}
	for mode := ModeEnact; mode < modeCount; mode++ {
		fmt.Fprintf(os.Stderr, "%s%s stack[%s]: %d items\n", core.Prefix(), traceTag, modeNames[mode], len(m.Stacks[mode]))
		traceFileWrite("%s%s stack[%s]: %d items\n", core.PlainPrefix(), traceTagPlain, modeNames[mode], len(m.Stacks[mode]))
	}
	total := 0
	for _, name := range frameOrder {
		total += len(m.Frames[name])
	}
	fmt.Fprintf(os.Stderr, "%s%s frames: %d items, budget: %d, errors: %d\n",
		core.Prefix(), traceTag, total, m.APICallsRemaining, len(m.Errors))
	traceFileWrite("%s%s frames: %d items, budget: %d, errors: %d\n",
		core.PlainPrefix(), traceTagPlain, total, m.APICallsRemaining, len(m.Errors))
}

// TraceItem dumps item fields to stderr.
func TraceItem(label string, item Item) {
	if !Trace.Enabled {
		return
	}
	content := item.Content
	if len(content) > 80 {
		content = content[:80] + "..."
	}
	content = strings.ReplaceAll(content, "\n", "\\n")
	fmt.Fprintf(os.Stderr, "%s%s %s: source=%s depth=%d relax=%d content=%s\n",
		core.Prefix(), traceTag, label, item.Source, item.Depth, item.RelaxCount, content)
	traceFileWrite("%s%s %s: source=%s depth=%d relax=%d content=%s\n",
		core.PlainPrefix(), traceTagPlain, label, item.Source, item.Depth, item.RelaxCount, content)
}

// TracePause prompts on stderr and reads a line from stdin when interactive.
// Returns "" on Enter, or the typed input for debug commands.
func TracePause() string {
	if !Trace.Enabled || !Trace.Interactive {
		return ""
	}
	fmt.Fprintf(os.Stderr, "%s%s > ", core.Prefix(), debugTag)
	traceFileWrite("%s%s > ", core.PlainPrefix(), debugTagPlain)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

func debugPrint(format string, args ...any) {
	fmt.Fprintf(os.Stderr, core.Prefix()+format, args...)
	plainArgs := make([]any, len(args))
	copy(plainArgs, args)
	for i, a := range plainArgs {
		if s, ok := a.(string); ok && s == debugTag {
			plainArgs[i] = debugTagPlain
		}
	}
	traceFileWrite(core.PlainPrefix()+format, plainArgs...)
}

type snapshotData struct {
	Stacks  map[string][]Item         `json:"stacks"`
	Frames  map[string][]FrameElement `json:"frames"`
	Budget  int                       `json:"budget"`
	Timeout int                       `json:"timeout"`
	Errors  []string                  `json:"errors"`
}

func (m *Machine) stateSnapshot() string {
	stacks := make(map[string][]Item, modeCount)
	for mode := ModeEnact; mode < modeCount; mode++ {
		stacks[modeNames[mode]] = m.Stacks[mode]
	}
	data := snapshotData{
		Stacks:  stacks,
		Frames:  m.Frames,
		Budget:  m.APICallsRemaining,
		Timeout: m.TimeoutSeconds,
		Errors:  m.Errors,
	}
	b, _ := json.MarshalIndent(data, "", "  ") // infallible: marshaling known Go structs
	return string(b)
}

func debugHelp(filter string) {
	help := `commands:
  help [filter]                              show commands (optionally filtered)
  state                                      full machine state as JSON
  budget                                     show remaining API budget
  budget <value>                             set API budget
  stack list                                 show all stack contents
  stack clear <mode>                         clear stack (enact/learn/model/build)
  stack push <mode> <json>                   push item onto stack
  stack pop <mode>                           pop top item from stack
  frame list [type] [start] [end]            show frame elements (all types if no type)
  frame push <type> <value>                  push element onto frame
  frame pop <type>                           pop top element from frame
  frame remove <type> <start> [end]          remove elements [start,end) from frame
  frame replace <type> <start> [end] <target> move elements [start,end) to target frame
  frame clone <source> <target>              clone frame
  frame swap <first> <second>                swap two frames
  frame create <type> <json>                 create/replace frame from JSON array
  skip                                       skip current item
  (Enter)                                    continue`
	lower := strings.ToLower(filter)
	for _, line := range strings.Split(help, "\n") {
		if filter == "" || strings.Contains(strings.ToLower(line), lower) {
			debugPrint("%s %s\n", debugTag, line)
		}
	}
}

func (m *Machine) dumpAllFrames() {
	index := 0
	for _, name := range frameOrder {
		for _, e := range m.Frames[name] {
			debugPrint("  %02x [%s] %s\n", index, name, e.Value)
			index++
		}
	}
	if index == 0 {
		debugPrint("%s (empty)\n", debugTag)
	}
}

func (m *Machine) dumpFrame(name string, args []string) {
	frame := m.Frames[name]
	if len(args) == 0 {
		debugPrint("%s frame[%s] (%d elements):\n", debugTag, name, len(frame))
		for i, e := range frame {
			debugPrint("  %d: %s\n", i, e.Value)
		}
		return
	}
	start, err := strconv.Atoi(args[0])
	if err != nil {
		debugPrint("%s bad start: %s\n", debugTag, args[0])
		return
	}
	end := len(frame)
	if len(args) >= 2 {
		end, err = strconv.Atoi(args[1])
		if err != nil {
			debugPrint("%s bad end: %s\n", debugTag, args[1])
			return
		}
	}
	if start < 0 || end > len(frame) || start >= end {
		debugPrint("%s range [%d,%d) out of bounds (frame has %d elements)\n", debugTag, start, end, len(frame))
		return
	}
	debugPrint("%s frame[%s] [%d,%d):\n", debugTag, name, start, end)
	for i := start; i < end; i++ {
		debugPrint("  %d: %s\n", i, frame[i].Value)
	}
}

// DebugCommand processes a hard-coded debug command.
func (m *Machine) DebugCommand(input string) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return
	}
	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "help":
		filter := ""
		if len(args) > 0 {
			filter = args[0]
		}
		debugHelp(filter)

	case "state":
		debugPrint("%s\n%s\n", debugTag, m.stateSnapshot())

	case "budget":
		if len(args) == 0 {
			m.mu.Lock()
			remaining := m.APICallsRemaining
			m.mu.Unlock()
			debugPrint("%s budget: %d\n", debugTag, remaining)
			return
		}
		value, err := strconv.Atoi(args[0])
		if err != nil {
			debugPrint("%s bad number: %s\n", debugTag, args[0])
			return
		}
		m.mu.Lock()
		m.APICallsRemaining = value
		m.mu.Unlock()
		debugPrint("%s budget set to %d\n", debugTag, value)

	case "stack":
		if len(args) == 0 {
			debugPrint("%s stack requires: list, clear, push, or pop\n", debugTag)
			return
		}
		switch args[0] {
		case "list":
			for mode := ModeEnact; mode < modeCount; mode++ {
				debugPrint("%s stack[%s] (%d items):\n", debugTag, modeNames[mode], len(m.Stacks[mode]))
				for i, item := range m.Stacks[mode] {
					debugPrint("  %d: %s\n", i, strings.ReplaceAll(item.Content+item.Command, "\n", "\\n"))
				}
			}
		case "clear":
			if len(args) < 2 {
				debugPrint("%s stack clear requires a mode (enact/learn/model/build)\n", debugTag)
				return
			}
			if mode, ok := modeByName(args[1]); ok {
				m.Stacks[mode] = nil
				debugPrint("%s cleared stack[%s]\n", debugTag, args[1])
			} else {
				debugPrint("%s unknown mode: %s\n", debugTag, args[1])
			}
		case "push":
			if len(args) < 3 {
				debugPrint("%s stack push requires <mode> <json>\n", debugTag)
				return
			}
			mode, ok := modeByName(args[1])
			if !ok {
				debugPrint("%s unknown mode: %s\n", debugTag, args[1])
				return
			}
			itemJSON := strings.Join(args[2:], " ")
			var item Item
			if json.Unmarshal([]byte(itemJSON), &item) != nil {
				debugPrint("%s bad item JSON\n", debugTag)
				return
			}
			m.Stacks[mode] = append(m.Stacks[mode], item)
			debugPrint("%s pushed item to stack[%s]\n", debugTag, args[1])
		case "pop":
			if len(args) < 2 {
				debugPrint("%s stack pop requires a mode (enact/learn/model/build)\n", debugTag)
				return
			}
			if mode, ok := modeByName(args[1]); ok && len(m.Stacks[mode]) > 0 {
				m.Stacks[mode] = m.Stacks[mode][:len(m.Stacks[mode])-1]
				debugPrint("%s popped item from stack[%s]\n", debugTag, args[1])
			} else {
				debugPrint("%s stack empty or unknown mode: %s\n", debugTag, args[1])
			}
		default:
			debugPrint("%s unknown stack command: %s\n", debugTag, args[0])
		}

	case "frame":
		if len(args) == 0 {
			debugPrint("%s frame requires: list, push, pop, remove, replace, clone, swap, or create\n", debugTag)
			return
		}
		switch args[0] {
		case "list":
			if len(args) == 1 {
				m.dumpAllFrames()
			} else {
				m.dumpFrame(args[1], args[2:])
			}
		case "push":
			if len(args) < 3 {
				debugPrint("%s frame push requires <name> <value>\n", debugTag)
				return
			}
			name := args[1]
			value := strings.Join(args[2:], " ")
			m.framePush(name, FrameElement{Value: value, Level: LevelProc})
			debugPrint("%s pushed to frame[%s]\n", debugTag, name)
		case "pop":
			if len(args) < 2 {
				debugPrint("%s frame pop requires a frame name\n", debugTag)
				return
			}
			name := args[1]
			element := m.framePop(name)
			debugPrint("%s popped from frame[%s]: %s\n", debugTag, name, element.Value)
		case "remove":
			if len(args) < 3 {
				debugPrint("%s frame remove requires <name> <start> [end]\n", debugTag)
				return
			}
			name := args[1]
			start, err := strconv.Atoi(args[2])
			if err != nil {
				debugPrint("%s bad start: %s\n", debugTag, args[2])
				return
			}
			frame := m.Frames[name]
			end := len(frame)
			if len(args) >= 4 {
				end, err = strconv.Atoi(args[3])
				if err != nil {
					debugPrint("%s bad end: %s\n", debugTag, args[3])
					return
				}
			}
			if start < 0 || end > len(frame) || start >= end {
				debugPrint("%s range [%d,%d) out of bounds (frame has %d elements)\n", debugTag, start, end, len(frame))
				return
			}
			m.frameRemoveRange(name, start, end)
			debugPrint("%s removed [%d,%d) from frame[%s]\n", debugTag, start, end, name)
		case "replace":
			if len(args) < 4 {
				debugPrint("%s frame replace requires <name> <start> <target> or <name> <start> <end> <target>\n", debugTag)
				return
			}
			name := args[1]
			start, err := strconv.Atoi(args[2])
			if err != nil {
				debugPrint("%s bad start: %s\n", debugTag, args[2])
				return
			}
			frame := m.Frames[name]
			var end int
			var target string
			if len(args) >= 5 {
				end, err = strconv.Atoi(args[3])
				if err != nil {
					debugPrint("%s bad end: %s\n", debugTag, args[3])
					return
				}
				target = args[4]
			} else {
				end = len(frame)
				target = args[3]
			}
			if start < 0 || end > len(frame) || start >= end {
				debugPrint("%s range [%d,%d) out of bounds (frame has %d elements)\n", debugTag, start, end, len(frame))
				return
			}
			m.frameReplaceRange(name, start, end, target)
			debugPrint("%s moved [%d,%d) from frame[%s] to frame[%s]\n", debugTag, start, end, name, target)
		case "clone":
			if len(args) < 3 {
				debugPrint("%s frame clone requires <source> <target>\n", debugTag)
				return
			}
			m.frameClone(args[1], args[2])
			debugPrint("%s cloned frame[%s] to frame[%s]\n", debugTag, args[1], args[2])
		case "swap":
			if len(args) < 3 {
				debugPrint("%s frame swap requires <first> <second>\n", debugTag)
				return
			}
			m.frameSwap(args[1], args[2])
			debugPrint("%s swapped frame[%s] and frame[%s]\n", debugTag, args[1], args[2])
		case "create":
			if len(args) < 3 {
				debugPrint("%s frame create requires <name> <json>\n", debugTag)
				return
			}
			name := args[1]
			frameJSON := strings.Join(args[2:], " ")
			var elements []FrameElement
			if json.Unmarshal([]byte(frameJSON), &elements) != nil {
				debugPrint("%s bad frame JSON\n", debugTag)
				return
			}
			m.frameCreate(name, elements)
			debugPrint("%s created frame[%s] with %d elements\n", debugTag, name, len(elements))
		default:
			debugPrint("%s unknown frame command: %s\n", debugTag, args[0])
		}

	case "skip":
		debugPrint("%s skipping current item\n", debugTag)

	default:
		debugPrint("%s unknown command: %s (type 'help' for commands)\n", debugTag, cmd)
	}
}
