FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY *.go ./
RUN go build -o /reference-memory *.go

FROM alpine:3.20
RUN apk add --no-cache wget
COPY --from=builder /reference-memory /reference-memory
COPY .claw-describe.json /.claw-describe.json
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=6 \
    CMD wget -qO- http://localhost:8080/health > /dev/null || exit 1
CMD ["/reference-memory"]
