FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /claw-api ./cmd/claw-api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /claw-api /claw-api
HEALTHCHECK --interval=15s --timeout=5s --retries=3 \
  CMD ["/claw-api", "-healthcheck"]
ENTRYPOINT ["/claw-api"]
