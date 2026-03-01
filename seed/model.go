package main

import (
	"strings"

	"github.com/ctcrookertech/elmb/core"
)

type modelDirective struct {
	Op   string // "PLAN", "STEP", "INVESTIGATE", "REENACT", "DONE"
	Text string
}

func parseModelDirectives(response string) []modelDirective {
	var directives []modelDirective
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "DONE" {
			directives = append(directives, modelDirective{Op: "DONE"})
			continue
		}
		if strings.HasPrefix(line, "PLAN: ") {
			directives = append(directives, modelDirective{Op: "PLAN", Text: line[6:]})
			continue
		}
		if strings.HasPrefix(line, "STEP: ") {
			directives = append(directives, modelDirective{Op: "STEP", Text: line[6:]})
			continue
		}
		if strings.HasPrefix(line, "INVESTIGATE: ") {
			directives = append(directives, modelDirective{Op: "INVESTIGATE", Text: line[13:]})
			continue
		}
		if strings.HasPrefix(line, "REENACT: ") {
			directives = append(directives, modelDirective{Op: "REENACT", Text: line[9:]})
			continue
		}
		core.Line(core.Model, "skipping unrecognized line: "+line)
	}
	return directives
}

func (m *Machine) processModel(item Item) {
	if m.APIKey == "" {
		core.Line(core.Model, "no api key, passthrough")
		m.arise(ModeModel, item)
		return
	}

	frameCtx := m.frameText("")

	systemPrompt := "You are creating an actionable plan based on observations."

	prompt := "Current frame context:\n" + frameCtx + "\n" +
		"Observations to plan from:\n" + item.Content + "\n\n" +
		"Create a plan. Output one directive per line:\n" +
		"PLAN: <title> — the plan title\n" +
		"STEP: <action> — each concrete action step\n" +
		"REENACT: <command args...> — to execute a command through enact\n" +
		"INVESTIGATE: <question> — if more information is needed before planning\n" +
		"DONE — if no action is needed\n" +
		"Output only directives, no other text."

	core.Line(core.Model, "running planning infer call")
	result, err := m.inferWithSystem(systemPrompt, prompt)
	if err != nil {
		core.Errorf("model infer failed: %v", err)
		m.arise(ModeModel, item)
		return
	}

	directives := parseModelDirectives(result)

	var planParts []string
	hasPlan := false

	for _, d := range directives {
		switch d.Op {
		case "DONE":
			core.Line(core.Model, "no action needed")
			m.arise(ModeModel, item)
			return
		case "INVESTIGATE":
			core.Line(core.Model, "investigating: "+d.Text)
			m.relax(ModeModel, Item{Content: d.Text, Source: core.Model, RelaxCount: item.RelaxCount})
			return
		case "PLAN":
			core.Line(core.Model, "plan: "+d.Text)
			m.framePush("", FrameElement{Value: "plan: " + d.Text, Level: LevelTask})
			planParts = append(planParts, "PLAN: "+d.Text)
			hasPlan = true
		case "STEP":
			core.Line(core.Model, "step: "+d.Text)
			m.framePush("", FrameElement{Value: "step: " + d.Text, Level: LevelStep})
			planParts = append(planParts, "STEP: "+d.Text)
		case "REENACT":
			core.Line(core.Model, "reenact: "+d.Text)
			parts := strings.Fields(d.Text)
			if len(parts) > 0 {
				m.Stacks[ModeEnact] = append(m.Stacks[ModeEnact], Item{
					Command: parts[0],
					Args:    parts[1:],
					Source:  core.Model + ":reenact",
					Depth:   item.Depth,
				})
			}
		}
	}

	if !hasPlan {
		m.arise(ModeModel, item)
		return
	}

	m.arise(ModeModel, Item{
		Content:    strings.Join(planParts, "\n"),
		Source:     core.Model,
		Depth:      item.Depth,
		RelaxCount: item.RelaxCount,
	})
}
