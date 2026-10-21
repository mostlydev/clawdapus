//go:build spike

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const runsSpikeToolDescriptor = `{
 "version":2,
 "service":"fake-provider",
 "auth":{"type":"bearer","env":"TOOL_INGRESS_TOKEN"},
 "tools":[{
  "name":"echo",
  "description":"Return a test echo through the managed tool boundary.",
  "inputSchema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]},
  "http":{"method":"POST","path":"/tool/{claw_id}","body":"json"}
 }]
}`

func runsSpikeUnauthenticatedToolStatus(t *testing.T, network, image string) int {
	t.Helper()
	const probe = `import urllib.request, urllib.error
request = urllib.request.Request("http://fake-provider:8080/tool/unauthenticated", data=b'{}', headers={"Content-Type":"application/json"})
try:
    print(urllib.request.urlopen(request, timeout=10).status)
except urllib.error.HTTPError as error:
    print(error.code)
`
	out, err := exec.Command("docker", "run", "--rm", "--network", network, image, "python", "-c", probe).Output()
	if err != nil {
		t.Fatalf("unauthenticated tool probe: %v", err)
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return status
}

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
