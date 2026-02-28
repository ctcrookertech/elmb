package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/ctcrookertech/elmb/core"
)

func main() {
	if len(os.Args) < 3 {
		core.Errorf("usage: infer <key> <text...>")
		os.Exit(1)
	}

	key := os.Args[1]
	input := strings.Join(os.Args[2:], " ")

	body, _ := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4-6",
		"max_tokens": 4096,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": input}},
	})

	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
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
		core.Errorf("HTTP %d", resp.StatusCode)
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
			continue
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
		core.Print(event.Delta.Text)
	}

	if first {
		stopProgress()
	}

	core.BlockEnd()
}
