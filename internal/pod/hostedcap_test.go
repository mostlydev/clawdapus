package pod

import (
	"strings"
	"testing"
)

const hostedPod = `
x-claw:
  pod: hosted
services:
  researcher:
    image: researcher:latest
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      hosted-capabilities: [web_search, code_interpreter]
  writer:
    image: writer:latest
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
`

func TestParseHostedCapabilities(t *testing.T) {
	p, err := Parse(strings.NewReader(hostedPod))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Services["researcher"].Claw.HostedCapabilities; strings.Join(got, ",") != "code_interpreter,web_search" {
		t.Fatalf("researcher = %v", got)
	}
	// The grant is the declaring service's alone.
	if got := p.Services["writer"].Claw.HostedCapabilities; got != nil {
		t.Fatalf("writer = %v", got)
	}

	for name, edit := range map[string]string{
		"remote server":   "[mcp]",
		"unknown name":    "[web_search, browser]",
		"wildcard":        `["*"]`,
		"wrong case":      "[Web_Search]",
		"listed twice":    "[web_search, web_search]",
		"not a list":      "web_search",
		"a map":           "{web_search: true}",
		"non-string item": "[[web_search]]",
	} {
		if _, err := Parse(strings.NewReader(strings.Replace(hostedPod, "[web_search, code_interpreter]", edit, 1))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, edit := range map[string]string{"empty list": "[]", "null": "null"} {
		p, err := Parse(strings.NewReader(strings.Replace(hostedPod, "[web_search, code_interpreter]", edit, 1)))
		if err != nil || p.Services["researcher"].Claw.HostedCapabilities != nil {
			t.Errorf("%s: %v %v", name, err, p)
		}
	}
}

func TestPerRunServiceCannotDeclareHostedCapabilities(t *testing.T) {
	perRun := `
x-claw:
  pod: hosted
services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
      cllama: passthrough
      hosted-capabilities: [web_search]
`
	if _, err := Parse(strings.NewReader(perRun)); err == nil || !strings.Contains(err.Error(), "not supported for a per-run service") {
		t.Fatalf("err = %v", err)
	}
	// Without the field the same service parses.
	if _, err := Parse(strings.NewReader(strings.Replace(perRun, "      hosted-capabilities: [web_search]\n", "", 1))); err != nil {
		t.Fatalf("per-run service without a grant: %v", err)
	}
}
