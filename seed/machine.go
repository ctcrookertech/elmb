package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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

// FrameLevel indicates the granularity of a frame element.
type FrameLevel int

const (
	LevelBase FrameLevel = iota
	LevelProc
	LevelTask
	LevelStep
)

// FrameElement is a typed element in a frame.
type FrameElement struct {
	Value string
	Level FrameLevel
}

// Item is a work unit on a mode stack.
type Item struct {
	Content string
	Source  string
	Command string
	Args    []string
	Depth   int
}

// SpawnSpec describes a child elmb instance to spawn.
type SpawnSpec struct {
	Limit     Mode
	Command   string
	Args      []string
	BaseFrame string
	Stdin     string
}

// Machine holds the full ELMB state.
type Machine struct {
	Stacks [modeCount][]Item
	Frames map[string][]FrameElement
	Limit  Mode
	APIKey string
}

// NewMachine creates a machine with the seed action on the enact stack.
func NewMachine(limit Mode, apiKey string, command string, args []string) *Machine {
	m := &Machine{
		Frames: map[string][]FrameElement{},
		Limit:  limit,
		APIKey: apiKey,
	}
	seed := Item{Command: command, Args: args, Source: "seed"}
	m.Stacks[ModeEnact] = append(m.Stacks[ModeEnact], seed)
	m.framePush("", FrameElement{Value: command + " " + strings.Join(args, " "), Level: LevelBase})
	return m
}

// Run executes the processing loop until all stacks are empty.
func (m *Machine) Run() {
	for {
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
	defaultFrame := m.Frames[""]
	core.Line(core.Frame, "done, frame has "+strconv.Itoa(len(defaultFrame))+" items")
	core.BlockStart()
	for _, elem := range defaultFrame {
		core.Print(elem.Value)
		core.Print("\n")
	}
	core.BlockEnd()
}

// drain processes all items on a mode's stack before returning.
func (m *Machine) drain(mode Mode) {
	for len(m.Stacks[mode]) > 0 {
		item := m.Stacks[mode][len(m.Stacks[mode])-1]
		m.Stacks[mode] = m.Stacks[mode][:len(m.Stacks[mode])-1]
		core.Line(modeTag[mode], "processing: "+strings.ReplaceAll(item.Content+item.Command, "\n", " "))
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
		m.framePush("", FrameElement{Value: item.Content, Level: LevelProc})
		return
	}
	upper := from + 1
	core.Line(core.Arise, modeNames[from]+" → "+modeNames[upper]+": "+strings.ReplaceAll(item.Content, "\n", " "))
	m.Stacks[upper] = append(m.Stacks[upper], item)
}

// relax moves an item down to the next lower mode.
func (m *Machine) relax(from Mode, item Item) {
	if from <= ModeEnact {
		core.Errorf("cannot relax below enact")
		return
	}
	lower := from - 1
	core.Line(core.Relax, modeNames[from]+" → "+modeNames[lower]+": "+strings.ReplaceAll(item.Content, "\n", " "))
	m.Stacks[lower] = append(m.Stacks[lower], item)
}

// processEnact runs a command, captures output, adds to frame, arises to learn.
func (m *Machine) processEnact(item Item) {
	core.Line(core.Enact, "running: "+item.Command)
	result, err := m.runCommand(item.Command, item.Args)
	if err != nil {
		core.Errorf("enact failed: %v", err)
		return
	}
	core.Line(core.Frame, "add: "+strings.ReplaceAll(result, "\n", " "))
	m.framePush("", FrameElement{Value: result, Level: LevelProc})
	m.arise(ModeEnact, Item{Content: result, Source: core.Enact})
}

// processLearn, processModel, processBuild are in learn.go, model.go, build.go.

// --- Frame operations ---

func (m *Machine) framePush(name string, elem FrameElement) {
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

func (m *Machine) runCommandWithInput(command string, args []string, stdin string) (string, error) {
	bin := siblingPath(command)
	stopProgress := core.StartProgress()
	cmd := exec.Command(bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	stopProgress()
	core.Newline()
	if err != nil {
		return "", err
	}
	return parseOutputBlock(string(out)), nil
}

func (m *Machine) inferDirect(prompt string) (string, error) {
	return m.runCommandWithInput("infer", []string{m.APIKey, "-"}, prompt)
}

// --- Subprocess execution ---

func (m *Machine) runCommand(command string, args []string) (string, error) {
	bin := siblingPath(command)
	stopProgress := core.StartProgress()
	out, err := exec.Command(bin, args...).Output()
	stopProgress()
	core.Newline()
	if err != nil {
		return "", err
	}
	return parseOutputBlock(string(out)), nil
}

// --- Child spawning ---

func (m *Machine) spawnSync(spec SpawnSpec) (string, error) {
	bin := siblingPath("elmb")
	cliArgs := append([]string{"--plain", "--limit", modeNames[spec.Limit], spec.Command}, spec.Args...)
	stopProgress := core.StartProgress()
	cmd := exec.Command(bin, cliArgs...)
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}
	out, err := cmd.Output()
	stopProgress()
	core.Newline()
	if err != nil {
		return "", err
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
			cliArgs := append([]string{"--plain", "--limit", modeNames[s.Limit], s.Command}, s.Args...)
			cmd := exec.CommandContext(ctx, bin, cliArgs...)
			if s.Stdin != "" {
				cmd.Stdin = strings.NewReader(s.Stdin)
			}
			out, err := cmd.Output()
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
			cliArgs := append([]string{"--plain", "--limit", modeNames[s.Limit], s.Command}, s.Args...)
			cmd := exec.CommandContext(ctx, bin, cliArgs...)
			if s.Stdin != "" {
				cmd.Stdin = strings.NewReader(s.Stdin)
			}
			out, err := cmd.Output()
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

func siblingPath(name string) string {
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
		if trimmed == "[  output]" {
			capturing = true
			continue
		}
		if trimmed == "[exoutput]" {
			break
		}
		if capturing {
			content = append(content, line)
		}
	}
	return strings.Join(content, "\n")
}

