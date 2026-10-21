//go:build spike

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

const runsSpikeToolDescriptor = `{
 "version":2,
 "service":"fake-provider",
 "tools":[{
  "name":"echo",
  "description":"Return a test echo through the managed tool boundary.",
  "inputSchema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]},
  "http":{"method":"POST","path":"/tool/{claw_id}","body":"json"}
 }]
}`

func runsSpikeToolEvents(t *testing.T, generated string) string {
	t.Helper()
	out, err := exec.Command("docker", "compose", "-f", generated, "logs", "--no-log-prefix", "fake-provider").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func runsSpikeHasDispatch(raw []byte, id string) bool {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		var event struct {
			ClawID string `json:"claw_id"`
			Type   string `json:"type"`
			Status string `json:"dispatch_status"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.ClawID == id && event.Type == "tool_dispatch" && event.Status == "admitted" {
			return true
		}
	}
	return false
}
