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
	Reenact    bool
	Command    string
	Args       []string
}

func parseBuildSteps(content string) []buildStep {
	var steps []buildStep
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "REENACT: ") {
			rest := line[9:]
			parts := strings.Fields(rest)
			if len(parts) == 0 {
				continue
			}
			steps = append(steps, buildStep{
				Text:    rest,
				Reenact: true,
				Command: parts[0],
				Args:    parts[1:],
			})
			continue
		}
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
		m.framePush(FrameTask, FrameElement{Value: item.Content, Level: LevelTask})
		return
	}

	steps := parseBuildSteps(item.Content)
	if len(steps) == 0 {
		core.Line(core.Build, "no steps found, adding to frame")
		m.framePush(FrameTask, FrameElement{Value: item.Content, Level: LevelTask})
		return
	}

	core.Line(core.Build, "executing "+strconv.Itoa(len(steps))+" steps at depth "+strconv.Itoa(item.Depth))

	var failedSteps []buildStep

	for i, step := range steps {
		core.Line(core.Build, "step "+strconv.Itoa(i+1)+"/"+strconv.Itoa(len(steps))+": "+step.Text)

		if step.Reenact {
			core.Line(core.Build, "reenacting: "+step.Command+" "+strings.Join(step.Args, " "))
			m.Stacks[ModeEnact] = append(m.Stacks[ModeEnact], Item{
				Command: step.Command,
				Args:    step.Args,
				Source:  core.Build + ":reenact",
				Depth:   item.Depth,
			})
			continue
		}

		if !step.Actionable {
			core.Line(core.Build, "informational, adding to frame")
			m.framePush(FrameStep, FrameElement{Value: step.Text, Level: LevelStep})
			continue
		}

		core.Line(core.Build, "spawning child elmb for: "+step.Text)
		result, err := m.spawnSync(SpawnSpec{
			Limit:   ModeModel,
			Command: "infer",
			Args:    []string{"-"},
			Stdin:   step.Text,
		})
		if err != nil {
			core.Errorf("build step failed: %v", err)
			failedSteps = append(failedSteps, step)
			continue
		}

		core.Line(core.Build, "step completed, adding result to frame")
		m.framePush(FrameStep, FrameElement{Value: result, Level: LevelStep})
	}

	if len(failedSteps) > 0 && item.Depth < maxBuildDepth {
		core.Line(core.Build, "re-queuing "+strconv.Itoa(len(failedSteps))+" failed steps at depth "+strconv.Itoa(item.Depth+1))
		var lines []string
		for _, f := range failedSteps {
			lines = append(lines, "STEP: "+f.Text)
		}
		m.Stacks[ModeBuild] = append(m.Stacks[ModeBuild], Item{
			Content: strings.Join(lines, "\n"),
			Source:  core.Build,
			Depth:   item.Depth + 1,
		})
		return
	}

	if len(failedSteps) > 0 {
		core.Line(core.Build, "finalizing with "+strconv.Itoa(len(failedSteps))+" unresolved failures")
		for _, f := range failedSteps {
			m.framePush(FrameStep, FrameElement{Value: "failed: " + f.Text, Level: LevelStep})
		}
	}
}
