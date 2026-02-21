# surface-fleet-master

This is an operator-provided service skill override.

## Host

- **Hostname:** fleet-master
- **Network:** claw-internal
- **Expected port:** 8080 (or override with service metadata)

## Usage notes

- Use `GET /health` for readiness checks.
- Prefer authenticated client headers if available via environment variables.
- This file overrides the service-emitted `surface-fleet-master.md` when present.
