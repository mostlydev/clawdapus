FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN CGO_ENABLED=0 go build -o /clawctl ./cmd/clawctl

FROM gcr.io/distroless/static-debian12
COPY --from=build /clawctl /clawctl
EXPOSE 8082
HEALTHCHECK --interval=15s --timeout=5s --retries=3 \
  CMD ["/clawctl", "-healthcheck"]
ENTRYPOINT ["/clawctl"]
