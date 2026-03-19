package clawapi

import (
	"fmt"

	"github.com/mostlydev/clawdapus/internal/skillmd"
)

func GenerateServiceSkill(port string) string {
	body := fmt.Sprintf(
		"# claw-api\n\n"+
			"## Connection\n"+
			"- Hostname: claw-api\n"+
			"- Port: %s\n"+
			"- Base URL: http://claw-api:%s\n"+
			"- Authentication: runtime-projected bearer credential\n\n"+
			"## Read Operations\n"+
			"- `GET /fleet/status` returns scoped service health and uptime\n"+
			"- `GET /fleet/metrics?claw_id=<id>&since=<duration-or-rfc3339>` returns normalized telemetry for one claw\n"+
			"- `GET /fleet/logs?service=<name>&lines=<n>` returns recent logs for one in-scope service\n"+
			"- `GET /fleet/alerts?since=<duration-or-rfc3339>` returns anomaly summaries only\n\n"+
			"## Usage\n"+
			"- Use the runtime-projected credential for explicit HTTP calls when a client layer exposes it\n"+
			"- Treat responses as scope-filtered; do not assume omitted services are healthy or visible\n"+
			"- Use `/fleet/alerts` for anomaly push and `/fleet/status`, `/fleet/metrics`, or `/fleet/logs` for detail pull\n",
		port,
		port,
	)

	return skillmd.Format(
		"surface-claw-api",
		"Read-only governance API for fleet telemetry, health, logs, and alerts.",
		body,
	)
}
