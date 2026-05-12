package pod

import (
	"strings"
	"testing"
)

func TestParsePodContextChannel(t *testing.T) {
	p, err := Parse(strings.NewReader(`
x-claw:
  context:
    channel:
      since: 24h
      limit: 40
      max-chars: 8192
      buffer: 500
services:
  trader:
    image: example/trader:latest
    x-claw:
      agent: ./AGENTS.md
      context:
        channel:
          since: 2h
          limit: 12
          max_chars: 4096
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Context == nil || p.Context.Channel == nil {
		t.Fatal("expected pod channel context config")
	}
	if got := p.Context.Channel.Since; got != "24h" {
		t.Fatalf("pod since = %q", got)
	}
	if got := p.Context.Channel.Limit; got != 40 {
		t.Fatalf("pod limit = %d", got)
	}
	if got := p.Context.Channel.MaxChars; got != 8192 {
		t.Fatalf("pod max chars = %d", got)
	}
	if got := p.Context.Channel.Buffer; got != 500 {
		t.Fatalf("pod buffer = %d", got)
	}

	trader := p.Services["trader"]
	if trader == nil || trader.Claw == nil || trader.Claw.Context == nil || trader.Claw.Context.Channel == nil {
		t.Fatal("expected service channel context config")
	}
	if got := trader.Claw.Context.Channel.Since; got != "2h" {
		t.Fatalf("service since = %q", got)
	}
	if got := trader.Claw.Context.Channel.Limit; got != 12 {
		t.Fatalf("service limit = %d", got)
	}
	if got := trader.Claw.Context.Channel.MaxChars; got != 4096 {
		t.Fatalf("service max chars = %d", got)
	}
}

func TestParsePodContextChannelRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "bad since",
			yaml: `
x-claw:
  context:
    channel:
      since: later
services:
  trader:
    image: example/trader:latest
    x-claw:
      agent: ./AGENTS.md
`,
			want: "since",
		},
		{
			name: "negative limit",
			yaml: `
x-claw:
  context:
    channel:
      limit: -1
services:
  trader:
    image: example/trader:latest
    x-claw:
      agent: ./AGENTS.md
`,
			want: "limit",
		},
		{
			name: "conflicting max chars aliases",
			yaml: `
x-claw:
  context:
    channel:
      max-chars: 2048
      max_chars: 4096
services:
  trader:
    image: example/trader:latest
    x-claw:
      agent: ./AGENTS.md
`,
			want: "max-chars",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.yaml))
			if err == nil {
				t.Fatal("expected parse error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParsePodContextChannelAwarenessIsNotPublicConfig(t *testing.T) {
	p, err := Parse(strings.NewReader(`
x-claw:
  context:
    channel-awareness:
      since: 12h
      limit: 60
      max-chars: 8192
services:
  trader:
    image: example/trader:latest
    x-claw:
      agent: ./AGENTS.md
      context:
        channel-awareness:
          since: 30m
          limit: 20
          max_chars: 2048
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Context != nil && p.Context.Channel != nil {
		t.Fatalf("channel-awareness should not populate pod context config: %+v", p.Context.Channel)
	}
	trader := p.Services["trader"]
	if trader == nil || trader.Claw == nil {
		t.Fatal("expected parsed trader claw block")
	}
	if trader.Claw.Context != nil && trader.Claw.Context.Channel != nil {
		t.Fatalf("channel-awareness should not populate service context config: %+v", trader.Claw.Context.Channel)
	}
}
