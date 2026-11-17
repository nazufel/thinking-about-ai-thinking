package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// canary marks the hidden scoring material. It must match the constant in
// scoring/hidden/gk. If it ever shows up in a transcript, the model saw the
// hidden tests and the run is void.
const canary = "EFFORT-DEMO-CANARY-7c41d9e2"

// Transcript is what the harness reads out of Claude Code's stream-json
// output for one run.
type Transcript struct {
	// ResultSeen reports whether the final result event arrived.
	ResultSeen bool
	// Subtype and IsError come from the result event.
	Subtype string
	IsError bool
	// StopReason is the model's last stop reason, when reported.
	StopReason string
	// FinalText is the model's closing message.
	FinalText string

	Turns             int
	CostUSD           float64
	OutputTokens      int64
	APIDurationMS     int64
	PermissionDenials int
	// DeniedCalls describes each tool call the permission system refused.
	DeniedCalls []string

	// ToolCalls counts every tool call the model made.
	ToolCalls       int
	ToolCallsByName map[string]int
	// RanTests reports whether any shell command ran the Go tests.
	RanTests bool
	// ThinkingBlocks counts the thinking blocks in the transcript.
	ThinkingBlocks int

	// CanarySeen reports whether the hidden scoring material leaked.
	CanarySeen bool
	// Unparsed counts lines that were not JSON objects.
	Unparsed int
}

// parseTranscript reads newline-delimited JSON events. It is tolerant: an
// event or field it does not know is skipped, so a change in the output
// format loses detail but does not fail the run.
func parseTranscript(r io.Reader) (*Transcript, error) {
	t := &Transcript{ToolCallsByName: map[string]int{}}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			t.addLine(line)
		}
		if err == io.EOF {
			return t, nil
		}
		if err != nil {
			return t, err
		}
	}
}

func (t *Transcript) addLine(line []byte) {
	if bytes.Contains(line, []byte(canary)) {
		t.CanarySeen = true
	}
	var ev map[string]any
	if json.Unmarshal(line, &ev) != nil {
		t.Unparsed++
		return
	}
	switch str(ev, "type") {
	case "assistant":
		msg := obj(ev, "message")
		if reason := str(msg, "stop_reason"); reason != "" {
			t.StopReason = reason
		}
		for _, raw := range arr(msg, "content") {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch str(block, "type") {
			case "tool_use":
				name := str(block, "name")
				t.ToolCalls++
				t.ToolCallsByName[name]++
				if command := str(obj(block, "input"), "command"); strings.Contains(command, "go test") {
					t.RanTests = true
				}
			case "thinking", "redacted_thinking":
				t.ThinkingBlocks++
			}
		}
	case "result":
		t.ResultSeen = true
		t.Subtype = str(ev, "subtype")
		t.IsError, _ = ev["is_error"].(bool)
		t.FinalText = str(ev, "result")
		t.Turns = int(num(ev, "num_turns"))
		t.CostUSD = num(ev, "total_cost_usd")
		t.APIDurationMS = int64(num(ev, "duration_api_ms"))
		t.OutputTokens = int64(num(obj(ev, "usage"), "output_tokens"))
		denials := arr(ev, "permission_denials")
		t.PermissionDenials = len(denials)
		t.DeniedCalls = nil
		for _, raw := range denials {
			if d, ok := raw.(map[string]any); ok {
				t.DeniedCalls = append(t.DeniedCalls, describeDenial(d))
			}
		}
		if reason := str(ev, "stop_reason"); reason != "" {
			t.StopReason = reason
		}
	}
}

// describeDenial summarizes a refused tool call in one line: the tool, and
// the model's own description of the call when it gave one, otherwise the
// start of the command.
func describeDenial(d map[string]any) string {
	tool := str(d, "tool_name")
	input := obj(d, "tool_input")
	detail := str(input, "description")
	if detail == "" {
		detail = strings.Join(strings.Fields(str(input, "command")), " ")
	}
	if len(detail) > 160 {
		detail = detail[:157] + "..."
	}
	if detail == "" {
		return tool
	}
	return tool + ": " + detail
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func num(m map[string]any, key string) float64 {
	f, _ := m[key].(float64)
	return f
}

func obj(m map[string]any, key string) map[string]any {
	o, _ := m[key].(map[string]any)
	return o
}

func arr(m map[string]any, key string) []any {
	a, _ := m[key].([]any)
	return a
}
