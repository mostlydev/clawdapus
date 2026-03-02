# Nanobot Example

Minimal Clawdapus project using `CLAW_TYPE nanobot`.

## Run

```bash
cp .env.example .env
# edit .env

claw build -t nanobot-example:latest .
claw up -f claw-pod.yml -d
```
