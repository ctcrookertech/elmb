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
}

// SpawnSpec describes a child elmb instance to spawn.
type SpawnSpec struct {
	Limit   Mode
	Command   string
	Args      []string
	BaseFrame string
}

// Machine holds the full ELMB state.
type Machine struct {
	Stacks  [modeCount][]Item
	Frames  map[string][]FrameElement
	Limit Mode
}

// NewMachine creates a machine with the seed action on the enact stack.
func NewMachine(limit Mode, command string, args []string) *Machine {
	m := &Machine{
		Frames:  map[string][]FrameElement{},
		Limit: limit,
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
		core.Line(modeTag[mode], "processing: "+truncate(item.Content+item.Command, 60))
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
		core.Line(core.Frame, "add: "+truncate(item.Content, 60))
		m.framePush("", FrameElement{Value: item.Content, Level: LevelProc})
		return
	}
	upper := from + 1
	core.Line(core.Arise, modeNames[from]+" → "+modeNames[upper]+": "+truncate(item.Content, 60))
	m.Stacks[upper] = append(m.Stacks[upper], item)
}

// relax moves an item down to the next lower mode.
func (m *Machine) relax(from Mode, item Item) {
	if from <= ModeEnact {
		core.Errorf("cannot relax below enact")
		return
	}
	lower := from - 1
	core.Line(core.Relax, modeNames[from]+" → "+modeNames[lower]+": "+truncate(item.Content, 60))
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
	core.Line(core.Frame, "add: "+truncate(result, 60))
	m.framePush("", FrameElement{Value: result, Level: LevelProc})
	m.arise(ModeEnact, Item{Content: result, Source: core.Enact})
}

// processLearn is a stub: traces receipt, arises to model.
func (m *Machine) processLearn(item Item) {
	core.Line(core.Learn, "received: "+truncate(item.Content, 60))
	m.arise(ModeLearn, item)
}

// processModel is a stub: traces receipt, arises to build.
func (m *Machine) processModel(item Item) {
	core.Line(core.Model, "received: "+truncate(item.Content, 60))
	m.arise(ModeModel, item)
}

// processBuild is a stub: traces receipt, arises (to frame at limit).
func (m *Machine) processBuild(item Item) {
	core.Line(core.Build, "received: "+truncate(item.Content, 60))
	m.arise(ModeBuild, item)
}

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

func (m *Machine) spawnSync(limit Mode, command string, args []string) (string, error) {
	bin := siblingPath("elmb")
	cliArgs := append([]string{"--limit", modeNames[limit], command}, args...)
	stopProgress := core.StartProgress()
	out, err := exec.Command(bin, cliArgs...).Output()
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
			results[i], errs[i] = m.spawnSync(s.Limit, s.Command, s.Args)
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
			cliArgs := append([]string{"--limit", modeNames[s.Limit], s.Command}, s.Args...)
			cmd := exec.CommandContext(ctx, bin, cliArgs...)
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

func (m *Machine) spawnAsync(specs []SpawnSpec) {
	for _, spec := range specs {
		go func(s SpawnSpec) {
			bin := siblingPath("elmb")
			cliArgs := append([]string{"--limit", modeNames[s.Limit], s.Command}, s.Args...)
			exec.Command(bin, cliArgs...).Run()
		}(spec)
	}
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

func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func parseOutputBlock(raw string) string {
	clean := stripANSI(raw)
	lines := strings.Split(clean, "\n")
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

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
