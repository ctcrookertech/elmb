package main

import (
	"strconv"
	"strings"

	"github.com/ctcrookertech/elmb/core"
)

const maxLearnDepth = 3
const compactThreshold = 10

type learnDirective struct {
	Op   string // "+", "-", "=", "RECURSE", "DONE"
	Text string
	Low  int // for "=" directives
	High int
}

func parseLearnDirectives(response string) []learnDirective {
	var directives []learnDirective
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "DONE" {
			directives = append(directives, learnDirective{Op: "DONE"})
			continue
		}
		if strings.HasPrefix(line, "+ ") {
			directives = append(directives, learnDirective{Op: "+", Text: line[2:]})
			continue
		}
		if strings.HasPrefix(line, "- ") {
			directives = append(directives, learnDirective{Op: "-", Text: line[2:]})
			continue
		}
		if strings.HasPrefix(line, "RECURSE: ") {
			directives = append(directives, learnDirective{Op: "RECURSE", Text: line[9:]})
			continue
		}
		if strings.HasPrefix(line, "= ") {
			rest := line[2:]
			colonIdx := strings.Index(rest, ": ")
			if colonIdx < 0 {
				core.Line(core.Learn, "skipping malformed = directive: "+line)
				continue
			}
			rangeStr := rest[:colonIdx]
			replacement := rest[colonIdx+2:]
			dashIdx := strings.Index(rangeStr, "-")
			if dashIdx < 0 {
				core.Line(core.Learn, "skipping malformed range: "+line)
				continue
			}
			low, err1 := strconv.Atoi(rangeStr[:dashIdx])
			high, err2 := strconv.Atoi(rangeStr[dashIdx+1:])
			if err1 != nil || err2 != nil {
				core.Line(core.Learn, "skipping bad range numbers: "+line)
				continue
			}
			directives = append(directives, learnDirective{Op: "=", Text: replacement, Low: low, High: high})
			continue
		}
		core.Line(core.Learn, "skipping unrecognized line: "+line)
	}
	return directives
}

func (m *Machine) processLearn(item Item) {
	if m.APIKey == "" {
		core.Line(core.Learn, "no api key, passthrough")
		m.arise(ModeLearn, item)
		return
	}

	frameCtx := m.frameText("")

	extractPrompt := "You are analyzing output from a command execution.\n\n" +
		"Current frame context:\n" + frameCtx + "\n" +
		"New content to analyze:\n" + item.Content + "\n\n" +
		"Extract key facts and observations. Output one directive per line:\n" +
		"+ <text> — to add a new fact to the frame\n" +
		"- <text> — to remove an existing fact that is now outdated or wrong\n" +
		"Output only directives, no other text."

	assessPrompt := "You are assessing whether deeper investigation is needed.\n\n" +
		"Current frame context:\n" + frameCtx + "\n" +
		"Content just analyzed:\n" + item.Content + "\n\n" +
		"Should we investigate further? Output one per line:\n" +
		"RECURSE: <question> — for each follow-up question worth investigating\n" +
		"DONE — if no further investigation needed\n" +
		"Output only directives, no other text."

	specs := []SpawnSpec{
		{Limit: ModeEnact, Command: "infer", Args: []string{m.APIKey, "-"}, Stdin: extractPrompt},
		{Limit: ModeEnact, Command: "infer", Args: []string{m.APIKey, "-"}, Stdin: assessPrompt},
	}

	core.Line(core.Learn, "running extract and assess infer calls")
	results, err := m.spawnAll(specs)
	if err != nil {
		core.Errorf("learn infer failed: %v", err)
		m.arise(ModeLearn, item)
		return
	}

	var observations []string

	extractDirectives := parseLearnDirectives(results[0])
	for _, d := range extractDirectives {
		switch d.Op {
		case "+":
			core.Line(core.Learn, "adding: "+d.Text)
			m.framePush("", FrameElement{Value: d.Text, Level: LevelProc})
			observations = append(observations, d.Text)
		case "-":
			core.Line(core.Learn, "removing: "+d.Text)
			m.frameRemoveMatching("", d.Text)
		}
	}

	assessDirectives := parseLearnDirectives(results[1])
	for _, d := range assessDirectives {
		if d.Op == "RECURSE" && item.Depth < maxLearnDepth {
			core.Line(core.Learn, "recursing at depth "+strconv.Itoa(item.Depth+1)+": "+d.Text)
			m.Stacks[ModeLearn] = append(m.Stacks[ModeLearn], Item{
				Content: d.Text,
				Source:  core.Learn,
				Depth:   item.Depth + 1,
			})
		}
	}

	if len(m.Frames[""]) > compactThreshold {
		m.compactFrame("")
	}

	summary := item.Content
	if len(observations) > 0 {
		summary = strings.Join(observations, "; ")
	}
	m.arise(ModeLearn, Item{Content: summary, Source: core.Learn, Depth: item.Depth})
}

func (m *Machine) frameRemoveMatching(name string, text string) {
	f := m.Frames[name]
	var kept []FrameElement
	for _, e := range f {
		if e.Value != text {
			kept = append(kept, e)
		}
	}
	m.Frames[name] = kept
}

func (m *Machine) compactFrame(name string) {
	f := m.Frames[name]
	if len(f) <= compactThreshold {
		return
	}

	if m.APIKey == "" {
		return
	}

	var b strings.Builder
	for i, e := range f {
		b.WriteString(strconv.Itoa(i))
		b.WriteString(": ")
		b.WriteString(e.Value)
		b.WriteString("\n")
	}

	prompt := "You are compacting a frame of " + strconv.Itoa(len(f)) + " entries.\n\n" +
		"Current entries:\n" + b.String() + "\n" +
		"Summarize these into fewer entries. Output one directive per line:\n" +
		"= <low>-<high>: <replacement> — replace entries low through high-1 with the replacement text\n" +
		"Keep the most important information. Output only directives."

	core.Line(core.Learn, "compacting frame with "+strconv.Itoa(len(f))+" entries")
	result, err := m.inferDirect(prompt)
	if err != nil {
		core.Errorf("compact infer failed: %v", err)
		return
	}

	directives := parseLearnDirectives(result)
	// Apply in reverse order so indices stay valid
	for i := len(directives) - 1; i >= 0; i-- {
		d := directives[i]
		if d.Op != "=" {
			continue
		}
		if d.Low < 0 || d.High > len(m.Frames[name]) || d.Low >= d.High {
			core.Line(core.Learn, "skipping out-of-bounds compact range")
			continue
		}
		core.Line(core.Learn, "compacting entries "+strconv.Itoa(d.Low)+"-"+strconv.Itoa(d.High)+": "+d.Text)
		replacement := FrameElement{Value: d.Text, Level: LevelProc}
		frame := m.Frames[name]
		newFrame := make([]FrameElement, 0, len(frame)-(d.High-d.Low)+1)
		newFrame = append(newFrame, frame[:d.Low]...)
		newFrame = append(newFrame, replacement)
		newFrame = append(newFrame, frame[d.High:]...)
		m.Frames[name] = newFrame
	}
}
