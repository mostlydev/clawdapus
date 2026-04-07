# PicoClaw Example

Minimal Clawdapus project using `CLAW_TYPE picoclaw`.

## Run

```bash
cp .env.example .env
# edit .env

claw pull -f claw-pod.yml
claw build -f claw-pod.yml
claw up -f claw-pod.yml -d
```
