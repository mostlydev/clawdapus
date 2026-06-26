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

func TestParsePodContextRuntimeReminders(t *testing.T) {
	p, err := Parse(strings.NewReader(`
x-claw:
  context:
    runtime-reminders:
      - id: operating-focus
        text: Keep the operating contract visible.
services:
  inherited:
    image: example/agent:latest
    x-claw:
      agent: ./AGENTS.md
  override:
    image: example/agent:latest
    x-claw:
      agent: ./AGENTS.md
      context:
        runtime-reminders:
          - id: local-focus
            text: Use the local reminder.
            enabled: false
            cadence: every_turn
            placement: before_feeds
            max_chars: 80
  suppress:
    image: example/agent:latest
    x-claw:
      agent: ./AGENTS.md
      context:
        runtime-reminders: []
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Context == nil || len(p.Context.RuntimeReminders) != 1 {
		t.Fatalf("expected pod runtime reminder config, got %+v", p.Context)
	}
	podReminder := p.Context.RuntimeReminders[0]
	if podReminder.ID != "operating-focus" || podReminder.MaxChars != 800 || !podReminder.Enabled || podReminder.Cadence != "every_turn" || podReminder.Placement != "before_feeds" {
		t.Fatalf("unexpected pod reminder defaults: %+v", podReminder)
	}

	inherited := p.Services["inherited"]
	if inherited == nil || inherited.Claw == nil || inherited.Claw.Context != nil {
		t.Fatalf("expected no service context override for inherited service, got %+v", inherited)
	}

	override := p.Services["override"]
	if override == nil || override.Claw == nil || override.Claw.Context == nil || len(override.Claw.Context.RuntimeReminders) != 1 {
		t.Fatalf("expected service runtime reminder override, got %+v", override)
	}
	local := override.Claw.Context.RuntimeReminders[0]
	if local.ID != "local-focus" || local.Enabled || local.MaxChars != 80 {
		t.Fatalf("unexpected service reminder: %+v", local)
	}

	suppress := p.Services["suppress"]
	if suppress == nil || suppress.Claw == nil || suppress.Claw.Context == nil || suppress.Claw.Context.RuntimeReminders == nil || len(suppress.Claw.Context.RuntimeReminders) != 0 {
		t.Fatalf("expected explicit empty runtime reminder override, got %+v", suppress)
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

func TestParsePodContextRuntimeRemindersRejectInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing id",
			yaml: `
x-claw:
  context:
    runtime-reminders:
      - text: Missing id.
services:
  agent:
    image: example/agent:latest
    x-claw:
      agent: ./AGENTS.md
`,
			want: "id",
		},
		{
			name: "duplicate id",
			yaml: `
x-claw:
  context:
    runtime-reminders:
      - id: focus
        text: One.
      - id: focus
        text: Two.
services:
  agent:
    image: example/agent:latest
    x-claw:
      agent: ./AGENTS.md
`,
			want: "duplicate",
		},
		{
			name: "unsupported cadence",
			yaml: `
x-claw:
  context:
    runtime-reminders:
      - id: focus
        text: One.
        cadence: min_interval
services:
  agent:
    image: example/agent:latest
    x-claw:
      agent: ./AGENTS.md
`,
			want: "cadence",
		},
		{
			name: "unsupported placement",
			yaml: `
x-claw:
  context:
    runtime-reminders:
      - id: focus
        text: One.
        placement: after_feeds
services:
  agent:
    image: example/agent:latest
    x-claw:
      agent: ./AGENTS.md
`,
			want: "placement",
		},
		{
			name: "oversized text",
			yaml: `
x-claw:
  context:
    runtime-reminders:
      - id: focus
        text: Too long.
        max_chars: 3
services:
  agent:
    image: example/agent:latest
    x-claw:
      agent: ./AGENTS.md
`,
			want: "max-chars",
		},
		{
			name: "conflicting max chars aliases",
			yaml: `
x-claw:
  context:
    runtime-reminders:
      - id: focus
        text: One.
        max-chars: 10
        max_chars: 20
services:
  agent:
    image: example/agent:latest
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
