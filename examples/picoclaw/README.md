# PicoClaw Example

Minimal Clawdapus project using `CLAW_TYPE picoclaw`.

## Run

```bash
cp .env.example .env
# edit .env

claw build -t picoclaw-example:latest .
claw up -f claw-pod.yml -d
```
