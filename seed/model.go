package main

import (
	"strings"

	"github.com/ctcrookertech/elmb/core"
)

type modelDirective struct {
	Op   string // "PLAN", "STEP", "INVESTIGATE", "DONE"
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

	prompt := "You are creating an actionable plan based on observations.\n\n" +
		"Current frame context:\n" + frameCtx + "\n" +
		"Observations to plan from:\n" + item.Content + "\n\n" +
		"Create a plan. Output one directive per line:\n" +
		"PLAN: <title> — the plan title\n" +
		"STEP: <action> — each concrete action step\n" +
		"INVESTIGATE: <question> — if more information is needed before planning\n" +
		"DONE — if no action is needed\n" +
		"Output only directives, no other text."

	core.Line(core.Model, "running planning infer call")
	result, err := m.inferDirect(prompt)
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
			m.relax(ModeModel, Item{Content: d.Text, Source: core.Model})
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
		}
	}

	if !hasPlan {
		m.arise(ModeModel, item)
		return
	}

	m.arise(ModeModel, Item{
		Content: strings.Join(planParts, "\n"),
		Source:  core.Model,
		Depth:   item.Depth,
	})
}
