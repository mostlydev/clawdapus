FROM node:24-bookworm-slim

ARG OPENCLAW_NPM_PACKAGE=openclaw@2026.2.9

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    cron \
    curl \
    git \
    jq \
    tini \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g "${OPENCLAW_NPM_PACKAGE}"

# Remove cron reminder relay boilerplate from OpenClaw prompt payloads.
RUN node -e "const fs=require('fs');const dir='/usr/local/lib/node_modules/openclaw/dist';for (const f of fs.readdirSync(dir)) {if (!/^health-format-.*\\.js$/.test(f)) continue; const p=dir+'/'+f; const s=fs.readFileSync(p,'utf8'); const n=s.replace(/const CRON_EVENT_PROMPT = \\\"[^\\\"]*\\\";/g,'const CRON_EVENT_PROMPT = \"\";'); if (n!==s) {fs.writeFileSync(p,n); console.log('patched', p);} }"

# Lock down openclaw package — defense in depth against runtime self-patching.
RUN chmod -R a-w /usr/local/lib/node_modules/openclaw/

WORKDIR /workspace

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/entrypoint.sh"]
