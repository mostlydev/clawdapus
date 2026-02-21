# Trading Desk Vertical Spike 2 (Codex)

This is a separate vertical spike from `examples/trading-desk`.

Goal: mirror the real `clawdbot@tiverton.local` desk shape while enforcing isolation.

- Not a single OpenClaw process with many agents
- One OpenClaw runtime per agent container
- Shared research and policy via a single shared volume
- Discord is the inter-agent coordination transport
- Trading API runs as a sibling service in the same pod
- Trading workflow skill comes from the API service itself

## Remote Layout Mapping

The spike mirrors the layout observed on `clawdbot@tiverton.local`:

- `~/clawd-shared/` -> `volume://trading-shared` mounted at `/mnt/trading-shared`
- `~/clawd-<agent>/` -> per-agent workspace volumes (`<agent>-workspace`)
- `~/trading-api/` -> `trading-api` service targeted with `service://trading-api`

## Services in this spike

- `postgres` (system of record backing store)
- `trading-api` (REST/system of record)
- `tiverton` (coordinator)
- `westin`, `logan`, `gerrard`, `dundas`, `boulton` (traders)
- `allen` (infra monitor)

Every claw service has:

- its own image and runtime config
- its own Discord account binding
- its own workspace volume
- shared access to `/mnt/trading-shared` (RW for traders/coordinator, RO for Allen)
- `service://trading-api` so the API can emit and own workflow skill docs

## File layout

- `claw-pod.yml`: pod topology
- `Clawfile.base`: shared OpenClaw image baseline
- `Clawfile.<agent>`: per-agent image overlays
- `agents/*.md`: agent contract files
- `skills/*.md`: operational skills
- `policy/*.md`: static policy docs mounted where needed
- `entrypoint.sh`: runner startup

## Build images

```bash
claw build -t claw-trading-base-2:latest examples/trading-desk-2/Clawfile.base
claw build -t claw-trading-tiverton-2:latest examples/trading-desk-2/Clawfile.tiverton
claw build -t claw-trading-westin-2:latest examples/trading-desk-2/Clawfile.westin
claw build -t claw-trading-logan-2:latest examples/trading-desk-2/Clawfile.logan
claw build -t claw-trading-gerrard-2:latest examples/trading-desk-2/Clawfile.gerrard
claw build -t claw-trading-dundas-2:latest examples/trading-desk-2/Clawfile.dundas
claw build -t claw-trading-boulton-2:latest examples/trading-desk-2/Clawfile.boulton
claw build -t claw-trading-allen-2:latest examples/trading-desk-2/Clawfile.allen
```

## Launch

```bash
cp examples/trading-desk-2/.env.example examples/trading-desk-2/.env
claw compose -f examples/trading-desk-2/claw-pod.yml up -d
```

## API-owned trading skill

This spike expects `trading-api` to publish the canonical trading workflow skill through image metadata:

- image label: `claw.skill.emit=/app/skills/surface-trading-api.md`
- consumers read it at: `/claw/skills/surface-trading-api.md`

That keeps trading workflow docs self-describing and versioned with the API service itself.

## Key design choices

This spike intentionally prioritizes process and filesystem isolation over convenience:

- each agent manages its own cron, model, and memory
- shared context is explicit through volume surfaces and service-emitted API skills
- inter-agent discovery is explicit through handles and generated `CLAW_HANDLE_*` env vars
