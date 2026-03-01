package core

import (
	"fmt"
	"os"
	"sync"
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

// TraceFile receives a plain-text copy of all output when set.
var TraceFile *os.File

var (
	startTime = time.Now()
	pid       = os.Getpid()
)

// Prefix returns the hex timestamp and PID prefix for trace lines.
// Format: "XXXXX XXXXX " where first is seconds since start, second is PID.
// Both values are masked to exactly 5 hex digits.
// Prefix returns the colored hex prefix for terminal output.
func Prefix() string {
	elapsed := int(time.Since(startTime).Seconds()) & 0xFFFFF
	if Plain {
		return fmt.Sprintf("%05X %05X ", elapsed, pid&0xFFFFF)
	}
	return fmt.Sprintf("\033[37m%05X\033[0m \033[90m%05X\033[0m ", elapsed, pid&0xFFFFF)
}

// PlainPrefix returns the uncolored hex prefix for trace files.
func PlainPrefix() string {
	elapsed := int(time.Since(startTime).Seconds()) & 0xFFFFF
	return fmt.Sprintf("%05X %05X ", elapsed, pid&0xFFFFF)
}

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

func plainTag(label string) string {
	return fmt.Sprintf("[%8s]", label)
}

// traceWrite writes plain text to the trace file if set.
func traceWrite(text string) {
	if TraceFile != nil {
		fmt.Fprint(TraceFile, text)
	}
}

func Line(label, text string) {
	fmt.Printf("%s%s %s\n", Prefix(), Tag(label), text)
	traceWrite(fmt.Sprintf("%s%s %s\n", PlainPrefix(), plainTag(label), text))
}

func BlockStart() {
	fmt.Printf("%s%s\n", Prefix(), Tag(Output))
	traceWrite(fmt.Sprintf("%s%s\n", PlainPrefix(), plainTag(Output)))
}

func BlockEnd() {
	fmt.Printf("\n%s%s\n", Prefix(), Tag(ExOutput))
	traceWrite(fmt.Sprintf("\n%s%s\n", PlainPrefix(), plainTag(ExOutput)))
}

func Print(text string) {
	fmt.Print(text)
	traceWrite(text)
}

func Newline() {
	fmt.Println()
	traceWrite("\n")
}

func Errorf(format string, args ...any) {
	Line(Error, fmt.Sprintf(format, args...))
}

// TracePrint writes text to the trace file only (no stdout/stderr).
func TracePrint(text string) {
	traceWrite(text)
}

var (
	progressMu     sync.Mutex
	progressActive bool
)

func StartProgress() func() {
	progressMu.Lock()
	if progressActive {
		progressMu.Unlock()
		return func() {}
	}
	progressActive = true
	progressMu.Unlock()

	done := make(chan struct{})
	go func() {
		fmt.Printf("%s%s ", Prefix(), Tag(Progress))
		traceWrite(fmt.Sprintf("%s%s ", PlainPrefix(), plainTag(Progress)))
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Print(".")
				traceWrite(".")
			}
		}
	}()
	return func() {
		close(done)
		progressMu.Lock()
		progressActive = false
		progressMu.Unlock()
	}
}
