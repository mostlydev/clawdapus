FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY examples/channel-memory ./examples/channel-memory
RUN CGO_ENABLED=0 go build -o /channel-memory ./examples/channel-memory

FROM alpine:3.20
RUN apk add --no-cache wget
COPY --from=builder /channel-memory /channel-memory
COPY examples/channel-memory/.claw-describe.json /.claw-describe.json
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=6 \
    CMD wget -qO- http://localhost:8080/health > /dev/null || exit 1
CMD ["/channel-memory"]
