FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /claw-mcp-stdio ./cmd/claw-mcp-stdio

FROM node:20-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tini python3 \
    && rm -rf /var/lib/apt/lists/*
ENV HOME=/tmp \
    NPM_CONFIG_CACHE=/tmp/.npm
COPY --from=build /claw-mcp-stdio /usr/local/bin/claw-mcp-stdio
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --retries=3 \
  CMD ["/usr/local/bin/claw-mcp-stdio", "-healthcheck"]
ENTRYPOINT ["tini", "--", "/usr/local/bin/claw-mcp-stdio"]
