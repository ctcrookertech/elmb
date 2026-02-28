package core

import (
	"fmt"
	"time"
)

const (
	Output   = "output"
	ExOutput = "exoutput"
	Progress = "progress"
	Error    = "error"
	Enact    = "enact"
	Learn    = "learn"
	Model    = "model"
	Build    = "build"
	Frame    = "frame"
	Arise    = "arise"
	Relax    = "relax"
)

// Plain disables ANSI color codes in all tag output.
var Plain bool

var tagColors = map[string]string{
	Output:   "\033[92m",
	ExOutput: "\033[32m",
	Progress: "\033[33m",
	Error:    "\033[31m",
	Enact:    "\033[36m",
	Learn:    "\033[34m",
	Model:    "\033[35m",
	Build:    "\033[97m",
	Frame:    "\033[96m",
	Arise:    "\033[93m",
	Relax:    "\033[37m",
}

func Tag(label string) string {
	if Plain {
		return fmt.Sprintf("[%8s]", label)
	}
	return fmt.Sprintf("%s[%8s]\033[0m", tagColors[label], label)
}

func Line(label, text string) {
	fmt.Printf("%s %s\n", Tag(label), text)
}

func BlockStart() {
	fmt.Printf("%s\n", Tag(Output))
}

func BlockEnd() {
	fmt.Printf("\n%s\n", Tag(ExOutput))
}

func Print(text string) {
	fmt.Print(text)
}

func Newline() {
	fmt.Println()
}

func Errorf(format string, args ...any) {
	Line(Error, fmt.Sprintf(format, args...))
}

func StartProgress() func() {
	done := make(chan struct{})
	go func() {
		fmt.Printf("%s ", Tag(Progress))
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Print(".")
			}
		}
	}()
	return func() { close(done) }
}
