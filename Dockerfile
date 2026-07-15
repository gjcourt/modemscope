# syntax=docker/dockerfile:1.7

FROM golang:1.23-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" \
    -o /out/modemscope-agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:latest AS runtime

COPY --from=builder /out/modemscope-agent /modemscope-agent

USER 65532:65532
EXPOSE 9104
ENTRYPOINT ["/modemscope-agent"]
