package clawapi

import (
	"fmt"

	"github.com/mostlydev/clawdapus/internal/skillmd"
)

func GenerateServiceSkill(port string) string {
	body := fmt.Sprintf(
		"# claw-api\n\n"+
			"## Connection\n"+
			"- Base URL: `$CLAW_API_URL` (http://claw-api:%s)\n"+
			"- Authentication: `Authorization: Bearer $CLAW_API_TOKEN`\n\n"+
			"## Example\n"+
			"```bash\n"+
			"curl -s -H \"Authorization: Bearer $CLAW_API_TOKEN\" \"$CLAW_API_URL/fleet/status\"\n"+
			"```\n\n"+
			"## Read Operations\n"+
			"- `GET /fleet/status` returns scoped service health and uptime\n"+
			"- `GET /fleet/metrics?claw_id=<id>&since=<duration-or-rfc3339>` returns normalized telemetry for one claw\n"+
			"- `GET /fleet/logs?service=<name>&lines=<n>` returns recent logs for one in-scope service\n"+
			"- `GET /fleet/alerts?since=<duration-or-rfc3339>` returns anomaly summaries only\n"+
			"- `GET /schedule` returns current scheduled invocation state for in-scope services\n"+
			"- `GET /schedule/<id>` returns detail for one scheduled invocation\n\n"+
			"## Usage\n"+
			"- Always include the bearer token header — unauthenticated requests return 401\n"+
			"- Responses are scope-filtered; do not assume omitted services are healthy or visible\n"+
			"- Use `/fleet/alerts` for anomaly push and `/fleet/status`, `/fleet/metrics`, or `/fleet/logs` for detail pull\n",
		port,
	)

	return skillmd.Format(
		"surface-claw-api",
		"Read-only governance API for fleet telemetry, health, logs, alerts, and schedule state.",
		body,
	)
}
