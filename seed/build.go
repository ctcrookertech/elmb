package main

import (
	"strconv"
	"strings"

	"github.com/ctcrookertech/elmb/core"
)

const maxBuildDepth = 2

type buildStep struct {
	Text       string
	Actionable bool
}

func parseBuildSteps(content string) []buildStep {
	var steps []buildStep
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "STEP: ") {
			continue
		}
		text := line[6:]
		actionable := looksActionable(text)
		steps = append(steps, buildStep{Text: text, Actionable: actionable})
	}
	return steps
}

func looksActionable(text string) bool {
	lower := strings.ToLower(text)
	actionVerbs := []string{"run ", "execute ", "call ", "invoke ", "spawn ", "create ", "build ", "compile ", "install ", "deploy "}
	for _, v := range actionVerbs {
		if strings.HasPrefix(lower, v) {
			return true
		}
	}
	return false
}

func (m *Machine) processBuild(item Item) {
	if item.Depth > maxBuildDepth {
		core.Line(core.Build, "max depth reached, finalizing")
		m.framePush("", FrameElement{Value: item.Content, Level: LevelTask})
		return
	}

	steps := parseBuildSteps(item.Content)
	if len(steps) == 0 {
		core.Line(core.Build, "no steps found, adding to frame")
		m.framePush("", FrameElement{Value: item.Content, Level: LevelTask})
		return
	}

	core.Line(core.Build, "executing "+strconv.Itoa(len(steps))+" steps at depth "+strconv.Itoa(item.Depth))

	var failures []string

	for i, step := range steps {
		core.Line(core.Build, "step "+strconv.Itoa(i+1)+"/"+strconv.Itoa(len(steps))+": "+step.Text)

		if !step.Actionable {
			core.Line(core.Build, "informational, adding to frame")
			m.framePush("", FrameElement{Value: step.Text, Level: LevelStep})
			continue
		}

		core.Line(core.Build, "spawning child elmb for: "+step.Text)
		result, err := m.spawnSync(SpawnSpec{
			Limit:   ModeModel,
			Command: "infer",
			Args:    []string{m.APIKey, "-"},
			Stdin:   step.Text,
		})
		if err != nil {
			core.Errorf("build step failed: %v", err)
			failures = append(failures, step.Text+": "+err.Error())
			continue
		}

		core.Line(core.Build, "step completed, adding result to frame")
		m.framePush("", FrameElement{Value: result, Level: LevelStep})
	}

	if len(failures) > 0 && item.Depth < maxBuildDepth {
		core.Line(core.Build, "re-queuing "+strconv.Itoa(len(failures))+" failed steps at depth "+strconv.Itoa(item.Depth+1))
		errorContext := "Previous failures:\n" + strings.Join(failures, "\n") + "\n\nOriginal plan:\n" + item.Content
		m.Stacks[ModeBuild] = append(m.Stacks[ModeBuild], Item{
			Content: errorContext,
			Source:  core.Build,
			Depth:   item.Depth + 1,
		})
		return
	}

	if len(failures) > 0 {
		core.Line(core.Build, "finalizing with "+strconv.Itoa(len(failures))+" unresolved failures")
		for _, f := range failures {
			m.framePush("", FrameElement{Value: "failed: " + f, Level: LevelStep})
		}
	}
}
