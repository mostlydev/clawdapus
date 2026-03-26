FROM python:3.11-slim

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    tini \
    procps \
    curl \
    git \
    && rm -rf /var/lib/apt/lists/*

# Pin hermes-agent to a known-good release
RUN pip install --no-cache-dir "hermes-agent[messaging,cron]==0.5.1"

# Apply compatibility patches before the image is sealed
COPY minisweagent_path.py /tmp/minisweagent_path.py
COPY patch-hermes-runtime.py /tmp/patch-hermes-runtime.py
RUN python3 /tmp/patch-hermes-runtime.py

COPY entrypoint.sh /usr/local/bin/hermes-entrypoint
RUN chmod +x /usr/local/bin/hermes-entrypoint

WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["hermes-entrypoint"]
