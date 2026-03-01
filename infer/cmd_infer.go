package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ctcrookertech/elmb/core"
)

func main() {
	key := os.Getenv("ELMB_API_KEY")
	if key == "" {
		core.Errorf("ELMB_API_KEY not set")
		os.Exit(1)
	}

	verbose := os.Getenv("ELMB_VERBOSE") != ""

	modelAliases := map[string]string{
		"sonnet": "claude-sonnet-4-6",
		"haiku":  "claude-haiku-4-5",
		"opus":   "claude-opus-4-6",
	}
	modelID := "claude-sonnet-4-6"

	args := os.Args[1:]
	for len(args) >= 2 && args[0] == "--model" {
		alias := args[1]
		if full, ok := modelAliases[alias]; ok {
			modelID = full
		} else {
			modelID = alias
		}
		args = args[2:]
	}

	if len(args) < 1 {
		core.Errorf("usage: infer [--model <alias|id>] <text...> or infer -")
		os.Exit(1)
	}

	var systemPrompt string
	var userMessage string

	if len(args) == 1 && args[0] == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			core.Errorf("reading stdin: %v", err)
			os.Exit(1)
		}
		input := string(raw)
		// Protocol: first line = system prompt, blank line separator, rest = user message
		if idx := strings.Index(input, "\n\n"); idx >= 0 {
			systemPrompt = input[:idx]
			userMessage = input[idx+2:]
		} else {
			userMessage = input
		}
	} else {
		userMessage = strings.Join(args, " ")
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "%s\033[90m[   trace]\033[0m infer: model=%s\n", core.Prefix(), modelID)
		if systemPrompt != "" {
			fmt.Fprintf(os.Stderr, "%s\033[90m[   trace]\033[0m infer: system=%s\n", core.Prefix(), systemPrompt)
		}
		fmt.Fprintf(os.Stderr, "%s\033[90m[   trace]\033[0m infer: prompt=%s\n", core.Prefix(), userMessage)
	}

	timeoutSec := 120
	if raw := os.Getenv("ELMB_TIMEOUT"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			timeoutSec = n
		}
	}

	messages := []map[string]string{{"role": "user", "content": userMessage}}
	reqBody := map[string]any{
		"model":      modelID,
		"max_tokens": 4096,
		"stream":     true,
		"messages":   messages,
	}
	if systemPrompt != "" {
		reqBody["system"] = systemPrompt
	}

	body, _ := json.Marshal(reqBody) // infallible: static types

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body)) // infallible: constant URL
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	stopProgress := core.StartProgress()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		stopProgress()
		core.Newline()
		core.Errorf("%v", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		stopProgress()
		core.Newline()
		respBody, _ := io.ReadAll(resp.Body) // best-effort: error context
		core.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		os.Exit(1)
	}

	first := true
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			break
		}
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue // best-effort: skip malformed SSE events
		}
		if event.Type != "content_block_delta" {
			continue
		}
		if first {
			stopProgress()
			core.Newline()
			core.BlockStart()
			first = false
		}
		text := event.Delta.Text
		text = strings.ReplaceAll(text, "[  output]", "[ _output]")
		text = strings.ReplaceAll(text, "[exoutput]", "[e_output]")
		core.Print(text)
	}

	if first {
		stopProgress()
		core.Newline()
	}

	if !first {
		core.BlockEnd()
	}
}
