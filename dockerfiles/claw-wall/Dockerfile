FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /claw-wall ./cmd/claw-wall

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /claw-wall /claw-wall
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --retries=3 \
  CMD ["/claw-wall", "-healthcheck"]
ENTRYPOINT ["/claw-wall"]
