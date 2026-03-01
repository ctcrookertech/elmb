package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ctcrookertech/elmb/core"
)

// Mode identifies one of the four ELMB modes.
type Mode int

const (
	ModeEnact Mode = iota
	ModeLearn
	ModeModel
	ModeBuild
	modeCount
)

var modeTag = [modeCount]string{
	ModeEnact: core.Enact,
	ModeLearn: core.Learn,
	ModeModel: core.Model,
	ModeBuild: core.Build,
}

var modeNames = [modeCount]string{
	ModeEnact: "enact",
	ModeLearn: "learn",
	ModeModel: "model",
	ModeBuild: "build",
}

func parseLimit(s string) Mode {
	for m := ModeEnact; m < modeCount; m++ {
		if modeNames[m] == s {
			return m
		}
	}
	return ModeBuild
}

func modeByName(name string) (Mode, bool) {
	for m := ModeEnact; m < modeCount; m++ {
		if modeNames[m] == name {
			return m, true
		}
	}
	return 0, false
}

// FrameLevel indicates the granularity of a frame element.
type FrameLevel int

const (
	LevelBase FrameLevel = iota
	LevelProc
	LevelTask
	LevelStep
)

// Frame type names and fixed concatenation order.
const (
	FrameBase  = "base"
	FrameInput = "input"
	FrameProc  = "proc"
	FrameTask  = "task"
	FrameStep  = "step"
)

var frameOrder = []string{FrameBase, FrameInput, FrameProc, FrameTask, FrameStep}

var validFrameTypes = map[string]bool{
	FrameBase: true, FrameInput: true, FrameProc: true, FrameTask: true, FrameStep: true,
}

// FrameElement is a typed element in a frame.
type FrameElement struct {
	Value string     `json:"value"`
	Level FrameLevel `json:"level"`
}

// Item is a work unit on a mode stack.
type Item struct {
	Content    string   `json:"content,omitempty"`
	Source     string   `json:"source,omitempty"`
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	Depth      int      `json:"depth,omitempty"`
	RelaxCount int      `json:"relax_count,omitempty"`
}

// SpawnSpec describes a child elmb instance to spawn.
type SpawnSpec struct {
	Limit   Mode
	Command string
	Args    []string
	Stdin   string
}

const (
	DefaultAPIBudget = 50
	DefaultTimeout   = 120
	MaxRelaxCount    = 5
)

// Machine holds the full ELMB state.
type Machine struct {
	Stacks            [modeCount][]Item
	Frames            map[string][]FrameElement
	Limit             Mode
	APIKey            string
	Config            *Config
	APICallsRemaining int
	TimeoutSeconds    int
	Errors            []string
	mu                sync.Mutex
}

// NewMachine creates a machine with the seed action on the enact stack.
func NewMachine(limit Mode, config *Config, command string, args []string, baseFrame string) *Machine {
	apiKey := config.ResolveAPIKey()

	budget := DefaultAPIBudget
	if raw := config.Resolve("ELMB_API_BUDGET"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			budget = n
		}
	}

	timeout := DefaultTimeout
	if raw := config.Resolve("ELMB_TIMEOUT"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			timeout = n
		}
	}

	m := &Machine{
		Frames:            map[string][]FrameElement{},
		Limit:             limit,
		APIKey:            apiKey,
		Config:            config,
		APICallsRemaining: budget,
		TimeoutSeconds:    timeout,
	}
	m.framePush(FrameBase, FrameElement{Value: "OS: " + runtime.GOOS + "/" + runtime.GOARCH, Level: LevelBase})
	if baseFrame != "" {
		m.framePush(FrameBase, FrameElement{Value: baseFrame, Level: LevelBase})
	}
	seed := Item{Command: command, Args: args, Source: "seed", Content: strings.Join(args, " ")}
	m.Stacks[ModeEnact] = append(m.Stacks[ModeEnact], seed)
	m.framePush(FrameInput, FrameElement{Value: strings.Join(args, " "), Level: LevelBase})
	return m
}

// Run executes the processing loop until all stacks are empty or budget is exhausted.
func (m *Machine) Run() error {
	for {
		m.mu.Lock()
		budgetLeft := m.APICallsRemaining
		m.mu.Unlock()
		if budgetLeft <= 0 {
			core.Line(core.Error, "API budget exhausted")
			m.Errors = append(m.Errors, "API budget exhausted")
			break
		}

		found := false
		for mode := ModeEnact; mode < modeCount; mode++ {
			if len(m.Stacks[mode]) == 0 {
				continue
			}
			found = true
			m.drain(mode)
			break
		}
		if !found {
			break
		}
	}

	// Output frame for parent capture
	total := 0
	for _, name := range frameOrder {
		total += len(m.Frames[name])
	}
	core.Line(core.Frame, "done, frame has "+strconv.Itoa(total)+" items")
	core.BlockStart()
	for _, name := range frameOrder {
		for _, elem := range m.Frames[name] {
			core.Print(elem.Value)
			core.Print("\n")
		}
	}
	core.BlockEnd()

	if len(m.Errors) > 0 {
		return fmt.Errorf("%d errors: %s", len(m.Errors), strings.Join(m.Errors, "; "))
	}
	return nil
}

// drain processes all items on a mode's stack before returning.
func (m *Machine) drain(mode Mode) {
	for len(m.Stacks[mode]) > 0 {
		m.mu.Lock()
		budgetLeft := m.APICallsRemaining
		m.mu.Unlock()
		if budgetLeft <= 0 {
			return
		}

		item := m.Stacks[mode][len(m.Stacks[mode])-1]
		m.Stacks[mode] = m.Stacks[mode][:len(m.Stacks[mode])-1]
		display := item.Content
		if item.Command != "" {
			display = item.Command + " " + strings.Join(item.Args, " ")
		}
		core.Line(modeTag[mode], "processing: "+strings.ReplaceAll(display, "\n", " "))

		TraceItem(modeNames[mode], item)
		TraceState(m)

		for {
			input := TracePause()
			if input == "" {
				break
			}
			m.DebugCommand(input)
		}

		switch mode {
		case ModeEnact:
			m.processEnact(item)
		case ModeLearn:
			m.processLearn(item)
		case ModeModel:
			m.processModel(item)
		case ModeBuild:
			m.processBuild(item)
		}
	}
}

// arise moves an item up to the next mode, or to frame if at limit.
func (m *Machine) arise(from Mode, item Item) {
	if from >= m.Limit {
		core.Line(core.Frame, "add: "+strings.ReplaceAll(item.Content, "\n", " "))
		m.framePush(FrameProc, FrameElement{Value: item.Content, Level: LevelProc})
		return
	}
	upper := from + 1
	core.Line(core.Arise, modeNames[from]+" → "+modeNames[upper]+": "+strings.ReplaceAll(item.Content, "\n", " "))
	m.Stacks[upper] = append(m.Stacks[upper], item)
}

// relax moves an item down to the next lower mode. Caps at MaxRelaxCount.
func (m *Machine) relax(from Mode, item Item) {
	if from <= ModeEnact {
		core.Errorf("cannot relax below enact")
		return
	}
	item.RelaxCount++
	if item.RelaxCount >= MaxRelaxCount {
		core.Line(core.Frame, "relax count exceeded, forcing to frame: "+strings.ReplaceAll(item.Content, "\n", " "))
		m.framePush(FrameProc, FrameElement{Value: item.Content, Level: LevelProc})
		return
	}
	lower := from - 1
	core.Line(core.Relax, modeNames[from]+" → "+modeNames[lower]+": "+strings.ReplaceAll(item.Content, "\n", " "))
	m.Stacks[lower] = append(m.Stacks[lower], item)
}

// processEnact runs a command, captures output, adds to frame, arises to learn.
func (m *Machine) processEnact(item Item) {
	if item.Source == "seed" {
		prompt := "Respond precisely, \"[ satisfy] \" + <single best guess description of what will satisfy the expressed intent, not an answer>, based on their command and parameters: " + item.Command + " " + strings.Join(item.Args, " ")
		satisfyResult, err := m.runCommandWithInput("infer", []string{"--model", "haiku", "-"}, prompt)
		if err != nil {
			core.Errorf("satisfy failed: %v", err)
			m.Errors = append(m.Errors, "satisfy: "+err.Error())
			return
		}
		satisfyText := strings.TrimPrefix(satisfyResult, "[ satisfy] ")
		core.Line(core.Satisfy, satisfyText)
	}

	core.Line(core.Enact, "running: "+item.Command)
	result, err := m.runCommand(item.Command, item.Args)
	if err != nil {
		errMsg := "Command failed: " + item.Command + " " + strings.Join(item.Args, " ") + "\nError: " + err.Error()
		core.Errorf("enact failed: %v", err)
		m.Errors = append(m.Errors, "enact: "+err.Error())
		m.arise(ModeEnact, Item{Content: errMsg, Source: core.Enact + ":error"})
		return
	}
	core.Line(core.Frame, "add: "+strings.ReplaceAll(result, "\n", " "))
	m.framePush(FrameProc, FrameElement{Value: result, Level: LevelProc})
	m.arise(ModeEnact, Item{Content: result, Source: core.Enact})
}

// processLearn, processModel, processBuild are in learn.go, model.go, build.go.

// --- Frame operations ---

func (m *Machine) framePush(name string, elem FrameElement) {
	if !validFrameTypes[name] {
		core.Errorf("invalid frame type: %s", name)
		return
	}
	m.Frames[name] = append(m.Frames[name], elem)
}

func (m *Machine) framePop(name string) FrameElement {
	f := m.Frames[name]
	if len(f) == 0 {
		return FrameElement{}
	}
	elem := f[len(f)-1]
	m.Frames[name] = f[:len(f)-1]
	return elem
}

func (m *Machine) frameRemoveRange(name string, low, high int) {
	f := m.Frames[name]
	m.Frames[name] = append(f[:low], f[high:]...)
}

func (m *Machine) frameReplaceRange(name string, low, high int, target string) {
	f := m.Frames[name]
	m.Frames[target] = append([]FrameElement{}, f[low:high]...)
	m.Frames[name] = append(f[:low], f[high:]...)
}

func (m *Machine) frameClone(src, dst string) {
	f := m.Frames[src]
	clone := make([]FrameElement, len(f))
	copy(clone, f)
	m.Frames[dst] = clone
}

func (m *Machine) frameSwap(a, b string) {
	m.Frames[a], m.Frames[b] = m.Frames[b], m.Frames[a]
}

func (m *Machine) frameCreate(name string, elems []FrameElement) {
	m.Frames[name] = elems
}

// --- Budget ---

// useAPICalls decrements the budget by n. Returns false if insufficient budget.
func (m *Machine) useAPICalls(n int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.APICallsRemaining < n {
		return false
	}
	m.APICallsRemaining -= n
	TraceLine("budget", "used "+strconv.Itoa(n)+", remaining: "+strconv.Itoa(m.APICallsRemaining))
	return true
}

// --- Infer helpers ---

func (m *Machine) frameText(name string) string {
	elems := m.Frames[name]
	if len(elems) == 0 {
		return "(empty frame)"
	}
	var b strings.Builder
	for i, e := range elems {
		b.WriteString(strconv.Itoa(i))
		b.WriteString(": ")
		b.WriteString(e.Value)
		b.WriteString("\n")
	}
	return b.String()
}

// contextText concatenates all typed frames in order for use in prompts.
func (m *Machine) contextText() string {
	var b strings.Builder
	index := 0
	for _, name := range frameOrder {
		for _, e := range m.Frames[name] {
			fmt.Fprintf(&b, "%02x [%s] %s\n", index, name, e.Value)
			index++
		}
	}
	if index == 0 {
		return "(empty context)"
	}
	return b.String()
}

// inferWithSystem calls the infer sibling with a system prompt and user message.
func (m *Machine) inferWithSystem(systemPrompt, userMessage string) (string, error) {
	if !m.useAPICalls(1) {
		return "", fmt.Errorf("API budget exhausted")
	}
	bin := siblingPath("infer")
	var stdin string
	if systemPrompt != "" {
		stdin = systemPrompt + "\n\n" + userMessage
	} else {
		stdin = "\n\n" + userMessage
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.TimeoutSeconds)*time.Second)
	defer cancel()

	stopProgress := core.StartProgress()
	cmd := exec.CommandContext(ctx, bin, "-")
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = m.childEnv()
	cmd.SysProcAttr = procGroupAttr()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	stopProgress()
	core.Newline()
	if Trace.Enabled && stderr.Len() > 0 {
		fmt.Fprint(os.Stderr, stderr.String())
		core.TracePrint(stderr.String())
	}
	if err != nil {
		errDetail := err.Error()
		if stderr.Len() > 0 {
			errDetail += ": " + stderr.String()
		}
		TraceLine("infer", "failed: "+errDetail)
		return "", fmt.Errorf("infer: %s", errDetail)
	}
	return parseOutputBlock(string(out)), nil
}

// inferDirect calls infer with no system prompt.
func (m *Machine) inferDirect(prompt string) (string, error) {
	return m.inferWithSystem("", prompt)
}

func (m *Machine) runCommandWithInput(command string, args []string, stdin string) (string, error) {
	bin := siblingPath(command)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.TimeoutSeconds)*time.Second)
	defer cancel()

	stopProgress := core.StartProgress()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = m.childEnv()
	cmd.SysProcAttr = procGroupAttr()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = os.Stdin
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	stopProgress()
	core.Newline()
	if Trace.Enabled && stderr.Len() > 0 {
		fmt.Fprint(os.Stderr, stderr.String())
		core.TracePrint(stderr.String())
	}
	if err != nil {
		errDetail := err.Error()
		if stderr.Len() > 0 {
			errDetail += ": " + stderr.String()
		}
		return "", fmt.Errorf("%s: %s", command, errDetail)
	}
	return parseOutputBlock(string(out)), nil
}

// --- Subprocess execution ---

func (m *Machine) runCommand(command string, args []string) (string, error) {
	return m.runCommandWithInput(command, args, "")
}

func (m *Machine) childEnv() []string {
	env := append(os.Environ(), "ELMB_API_KEY="+m.APIKey)
	if Trace.Enabled {
		env = append(env, "ELMB_VERBOSE=1")
	}
	return env
}

// --- Child spawning ---

func (m *Machine) spawnCLIArgs(spec SpawnSpec) []string {
	args := []string{"--plain", "--limit", modeNames[spec.Limit]}
	if Trace.Enabled {
		args = append(args, "--verbose")
	}
	if encoded := m.Config.EncodeValues(); encoded != "" {
		args = append(args, "--value", encoded)
	}
	args = append(args, spec.Command)
	args = append(args, spec.Args...)
	return args
}

func (m *Machine) spawnSync(spec SpawnSpec) (string, error) {
	if !m.useAPICalls(1) {
		return "", fmt.Errorf("API budget exhausted")
	}
	bin := siblingPath("elmb")
	cliArgs := m.spawnCLIArgs(spec)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.TimeoutSeconds)*time.Second)
	defer cancel()

	stopProgress := core.StartProgress()
	cmd := exec.CommandContext(ctx, bin, cliArgs...)
	cmd.Env = m.childEnv()
	cmd.SysProcAttr = procGroupAttr()
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	stopProgress()
	core.Newline()
	if Trace.Enabled && stderr.Len() > 0 {
		fmt.Fprint(os.Stderr, stderr.String())
		core.TracePrint(stderr.String())
	}
	if err != nil {
		errDetail := err.Error()
		if stderr.Len() > 0 {
			errDetail += ": " + stderr.String()
		}
		return "", fmt.Errorf("spawn: %s", errDetail)
	}
	return parseOutputBlock(string(out)), nil
}

func (m *Machine) spawnAll(specs []SpawnSpec) ([]string, error) {
	results := make([]string, len(specs))
	errs := make([]error, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func(i int, s SpawnSpec) {
			defer wg.Done()
			results[i], errs[i] = m.spawnSync(s)
		}(i, spec)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func (m *Machine) spawnAny(specs []SpawnSpec) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		output string
		err    error
	}
	ch := make(chan result, len(specs))
	for _, spec := range specs {
		go func(s SpawnSpec) {
			bin := siblingPath("elmb")
			cliArgs := m.spawnCLIArgs(s)
			innerCtx, innerCancel := context.WithTimeout(ctx, time.Duration(m.TimeoutSeconds)*time.Second)
			defer innerCancel()
			cmd := exec.CommandContext(innerCtx, bin, cliArgs...)
			cmd.Env = m.childEnv()
			cmd.SysProcAttr = procGroupAttr()
			if s.Stdin != "" {
				cmd.Stdin = strings.NewReader(s.Stdin)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if Trace.Enabled && stderr.Len() > 0 {
				fmt.Fprint(os.Stderr, stderr.String())
				core.TracePrint(stderr.String())
			}
			if err != nil {
				ch <- result{"", err}
				return
			}
			ch <- result{parseOutputBlock(string(out)), nil}
		}(spec)
	}
	r := <-ch
	return r.output, r.err
}

// AsyncResult holds the outcome of a single async child process.
type AsyncResult struct {
	Output string
	Err    error
	Done   bool
}

// AsyncHandle provides tracking and cancellation for async spawns.
type AsyncHandle struct {
	cancel  context.CancelFunc
	done    chan struct{}
	results []AsyncResult
	mu      sync.Mutex
}

func (h *AsyncHandle) Cancel() { h.cancel() }

func (h *AsyncHandle) AllDone() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

func (h *AsyncHandle) Results() []AsyncResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]AsyncResult, len(h.results))
	copy(out, h.results)
	return out
}

func (m *Machine) spawnAsync(specs []SpawnSpec) *AsyncHandle {
	ctx, cancel := context.WithCancel(context.Background())
	h := &AsyncHandle{
		cancel:  cancel,
		done:    make(chan struct{}),
		results: make([]AsyncResult, len(specs)),
	}
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func(i int, s SpawnSpec) {
			defer wg.Done()
			bin := siblingPath("elmb")
			cliArgs := m.spawnCLIArgs(s)
			innerCtx, innerCancel := context.WithTimeout(ctx, time.Duration(m.TimeoutSeconds)*time.Second)
			defer innerCancel()
			cmd := exec.CommandContext(innerCtx, bin, cliArgs...)
			cmd.Env = m.childEnv()
			cmd.SysProcAttr = procGroupAttr()
			if s.Stdin != "" {
				cmd.Stdin = strings.NewReader(s.Stdin)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if Trace.Enabled && stderr.Len() > 0 {
				fmt.Fprint(os.Stderr, stderr.String())
				core.TracePrint(stderr.String())
			}
			h.mu.Lock()
			h.results[i] = AsyncResult{
				Output: parseOutputBlock(string(out)),
				Err:    err,
				Done:   true,
			}
			h.mu.Unlock()
		}(i, spec)
	}
	go func() {
		wg.Wait()
		close(h.done)
	}()
	return h
}

// --- Helpers ---

var allowedSiblings = map[string]bool{"infer": true, "elmb": true}

func siblingPath(name string) string {
	if strings.ContainsAny(name, "/\\") || !allowedSiblings[name] {
		core.Errorf("rejected sibling path: %s", name)
		os.Exit(1)
	}
	exe, err := os.Executable()
	if err != nil {
		core.Errorf("cannot locate self: %v", err)
		os.Exit(1)
	}
	return filepath.Join(filepath.Dir(exe), name)
}

func parseOutputBlock(raw string) string {
	// Strip ANSI escape sequences as a safety net for external commands.
	var b strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] == '\033' && i+1 < len(raw) && raw[i+1] == '[' {
			j := i + 2
			for j < len(raw) && raw[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(raw[i])
		i++
	}
	lines := strings.Split(b.String(), "\n")
	var content []string
	capturing := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, "[  output]") {
			capturing = true
			continue
		}
		if strings.HasSuffix(trimmed, "[exoutput]") {
			break
		}
		if capturing {
			content = append(content, line)
		}
	}
	return strings.Join(content, "\n")
}
