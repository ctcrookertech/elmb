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
	fmt.Fprintf(os.Stderr, "%s%s frames: %d, budget: %d, errors: %d\n",
		core.Prefix(), traceTag, len(m.Frames), m.APICallsRemaining, len(m.Errors))
	traceFileWrite("%s%s frames: %d, budget: %d, errors: %d\n",
		core.PlainPrefix(), traceTagPlain, len(m.Frames), m.APICallsRemaining, len(m.Errors))
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
	fmt.Fprintf(os.Stderr, "%s%s press Enter to continue (or type a debug command): ", core.Prefix(), debugTag)
	traceFileWrite("%s%s press Enter to continue (or type a debug command): ", core.PlainPrefix(), debugTagPlain)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

const debugSystemPrompt = `You are a debugger for the ELMB state machine. You have access to the current machine state as JSON.

Available directives you can output (one per line):
DUMP_STACKS — show all stack contents
DUMP_FRAMES — show all frame names and sizes
DUMP_FRAME: <name> — show contents of a specific frame (use empty string for default)
SET_BUDGET: <n> — set API calls remaining to n
CLEAR_STACK: <mode> — clear a mode's stack (enact/learn/model/build)
PUSH_ITEM: <mode> <json> — push an item onto a mode's stack
POP_ITEM: <mode> — pop the top item from a mode's stack
SET_FRAME: <name> <json> — replace a frame's contents
FRAME_PUSH: <name> <value> — push element onto a frame
FRAME_POP: <name> — pop top element from a frame
FRAME_REMOVE_RANGE: <name> <low> <high> — remove elements [low, high)
FRAME_REPLACE_RANGE: <name> <low> <high> <target> — move range to target frame
FRAME_CLONE: <src> <dst> — deep copy src frame to dst
FRAME_SWAP: <a> <b> — swap two frames
FRAME_CREATE: <name> <json> — create/replace a frame from JSON array
OVERRIDE_RESPONSE: <text> — inject text as if it were an infer response
SKIP — skip processing the current item
CONTINUE — resume normal processing

Respond with directives only. If the user asks a question, answer it briefly then output CONTINUE.`

// DebugCommand processes an interactive debug input using infer.
func (m *Machine) DebugCommand(input string) {
	state := m.stateSnapshot()
	prompt := "Current machine state:\n" + state + "\n\nUser command: " + input
	result, err := m.inferWithSystem(debugSystemPrompt, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s%s infer error: %v\n", core.Prefix(), debugTag, err)
		traceFileWrite("%s%s infer error: %v\n", core.PlainPrefix(), debugTagPlain, err)
		return
	}
	fmt.Fprintf(os.Stderr, "%s%s response:\n%s\n", core.Prefix(), debugTag, result)
	traceFileWrite("%s%s response:\n%s\n", core.PlainPrefix(), debugTagPlain, result)
	m.applyDebugDirectives(result)
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

func (m *Machine) applyDebugDirectives(response string) {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case line == "DUMP_STACKS":
			for mode := ModeEnact; mode < modeCount; mode++ {
				debugPrint("%s stack[%s] (%d items):\n", debugTag, modeNames[mode], len(m.Stacks[mode]))
				for i, item := range m.Stacks[mode] {
					debugPrint("  %d: %s\n", i, strings.ReplaceAll(item.Content+item.Command, "\n", "\\n"))
				}
			}
		case line == "DUMP_FRAMES":
			for name, frame := range m.Frames {
				display := name
				if display == "" {
					display = "(default)"
				}
				debugPrint("%s frame[%s]: %d elements\n", debugTag, display, len(frame))
			}
		case strings.HasPrefix(line, "DUMP_FRAME: "):
			name := line[12:]
			frame := m.Frames[name]
			debugPrint("%s frame[%s] (%d elements):\n", debugTag, name, len(frame))
			for i, e := range frame {
				debugPrint("  %d: %s\n", i, e.Value)
			}
		case strings.HasPrefix(line, "SET_BUDGET: "):
			var n int
			if _, err := fmt.Sscanf(line[12:], "%d", &n); err != nil {
				debugPrint("%s SET_BUDGET parse error: %v\n", debugTag, err)
				continue
			}
			m.mu.Lock()
			m.APICallsRemaining = n
			m.mu.Unlock()
			debugPrint("%s budget set to %d\n", debugTag, n)
		case strings.HasPrefix(line, "CLEAR_STACK: "):
			modeName := line[13:]
			if mode, ok := modeByName(modeName); ok {
				m.Stacks[mode] = nil
				debugPrint("%s cleared stack[%s]\n", debugTag, modeName)
			}
		case strings.HasPrefix(line, "PUSH_ITEM: "):
			rest := line[11:]
			spaceIdx := strings.Index(rest, " ")
			if spaceIdx < 0 {
				continue
			}
			modeName := rest[:spaceIdx]
			itemJSON := rest[spaceIdx+1:]
			var item Item
			if json.Unmarshal([]byte(itemJSON), &item) != nil {
				continue
			}
			if mode, ok := modeByName(modeName); ok {
				m.Stacks[mode] = append(m.Stacks[mode], item)
				debugPrint("%s pushed item to stack[%s]\n", debugTag, modeName)
			}
		case strings.HasPrefix(line, "POP_ITEM: "):
			modeName := line[10:]
			if mode, ok := modeByName(modeName); ok && len(m.Stacks[mode]) > 0 {
				m.Stacks[mode] = m.Stacks[mode][:len(m.Stacks[mode])-1]
				debugPrint("%s popped item from stack[%s]\n", debugTag, modeName)
			}
		case strings.HasPrefix(line, "SET_FRAME: "):
			rest := line[11:]
			spaceIdx := strings.Index(rest, " ")
			if spaceIdx < 0 {
				continue
			}
			name := rest[:spaceIdx]
			frameJSON := rest[spaceIdx+1:]
			var elems []FrameElement
			if json.Unmarshal([]byte(frameJSON), &elems) != nil {
				continue
			}
			m.Frames[name] = elems
			debugPrint("%s set frame[%s] to %d elements\n", debugTag, name, len(elems))

		// --- Frame operations ---

		case strings.HasPrefix(line, "FRAME_PUSH: "):
			rest := line[12:]
			spaceIdx := strings.Index(rest, " ")
			if spaceIdx < 0 {
				continue
			}
			name := rest[:spaceIdx]
			value := rest[spaceIdx+1:]
			m.framePush(name, FrameElement{Value: value, Level: LevelProc})
			debugPrint("%s pushed to frame[%s]\n", debugTag, name)
		case strings.HasPrefix(line, "FRAME_POP: "):
			name := line[11:]
			elem := m.framePop(name)
			debugPrint("%s popped from frame[%s]: %s\n", debugTag, name, elem.Value)
		case strings.HasPrefix(line, "FRAME_REMOVE_RANGE: "):
			rest := line[20:]
			parts := strings.Fields(rest)
			if len(parts) != 3 {
				continue
			}
			name := parts[0]
			low, err1 := strconv.Atoi(parts[1])
			high, err2 := strconv.Atoi(parts[2])
			if err1 != nil || err2 != nil {
				continue
			}
			f := m.Frames[name]
			if low < 0 || high > len(f) || low >= high {
				debugPrint("%s FRAME_REMOVE_RANGE: out of bounds\n", debugTag)
				continue
			}
			m.frameRemoveRange(name, low, high)
			debugPrint("%s removed range [%d,%d) from frame[%s]\n", debugTag, low, high, name)
		case strings.HasPrefix(line, "FRAME_REPLACE_RANGE: "):
			rest := line[21:]
			parts := strings.Fields(rest)
			if len(parts) != 4 {
				continue
			}
			name := parts[0]
			low, err1 := strconv.Atoi(parts[1])
			high, err2 := strconv.Atoi(parts[2])
			target := parts[3]
			if err1 != nil || err2 != nil {
				continue
			}
			f := m.Frames[name]
			if low < 0 || high > len(f) || low >= high {
				debugPrint("%s FRAME_REPLACE_RANGE: out of bounds\n", debugTag)
				continue
			}
			m.frameReplaceRange(name, low, high, target)
			debugPrint("%s replaced range [%d,%d) from frame[%s] into frame[%s]\n", debugTag, low, high, name, target)
		case strings.HasPrefix(line, "FRAME_CLONE: "):
			rest := line[13:]
			parts := strings.Fields(rest)
			if len(parts) != 2 {
				continue
			}
			m.frameClone(parts[0], parts[1])
			debugPrint("%s cloned frame[%s] to frame[%s]\n", debugTag, parts[0], parts[1])
		case strings.HasPrefix(line, "FRAME_SWAP: "):
			rest := line[12:]
			parts := strings.Fields(rest)
			if len(parts) != 2 {
				continue
			}
			m.frameSwap(parts[0], parts[1])
			debugPrint("%s swapped frame[%s] and frame[%s]\n", debugTag, parts[0], parts[1])
		case strings.HasPrefix(line, "FRAME_CREATE: "):
			rest := line[14:]
			spaceIdx := strings.Index(rest, " ")
			if spaceIdx < 0 {
				continue
			}
			name := rest[:spaceIdx]
			frameJSON := rest[spaceIdx+1:]
			var elems []FrameElement
			if json.Unmarshal([]byte(frameJSON), &elems) != nil {
				continue
			}
			m.frameCreate(name, elems)
			debugPrint("%s created frame[%s] with %d elements\n", debugTag, name, len(elems))

		case strings.HasPrefix(line, "OVERRIDE_RESPONSE: "):
			debugPrint("%s override response noted (not applied in this context)\n", debugTag)
		case line == "SKIP":
			debugPrint("%s skip directive noted\n", debugTag)
		case line == "CONTINUE":
			// resume normal processing
		}
	}
}
