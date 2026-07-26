# modemscope — Prometheus exporter for Hitron DOCSIS cable modems

## Overview

modemscope polls a Hitron DOCSIS cable modem's status pages every scrape and
exposes the cable plant's vital signs — per-channel SNR, transmit/receive power,
FEC error counters, and the DOCSIS registration state — as Prometheus metrics.
None of that is retained by the modem, so an intermittent line problem otherwise
leaves no evidence. The load-bearing metric is `modemscope_uptime_seconds`:
every error counter resets on reboot, so uptime is what makes the counters
interpretable.

Verified against a **Hitron CODA-56** (DOCSIS 3.1) on Comcast. Serves metrics on
`:9104`.

## Layout

- `cmd/agent/` — entry point (`main.go`); wires collectors and serves `/metrics`.
- `internal/` — modem client and Prometheus collectors.
- `Dockerfile` — distroless static build (`CGO_ENABLED=0`, amd64).

## Develop

Go 1.23. Common tasks (see `Makefile`):

- `make test` — `go test ./...`
- `make tidy` — `go mod tidy`
- `make build` — `docker buildx build --platform=linux/amd64 --load -t ghcr.io/gjcourt/modemscope:dev .`
- `gofmt -l .` and `go vet ./...` — CI enforces both (see below)

CI (`.github/workflows/build.yml`) runs gofmt, `go vet`, `go test`, and a
`go mod tidy` diff check on every pull request and on push to `main`.

## Container image & deploy

Built and pushed to `ghcr.io/gjcourt/modemscope` by
`.github/workflows/build.yml` on push to `main` and via `workflow_dispatch`
(GHCR login uses `${{ secrets.GITHUB_TOKEN }}`). Tags emitted additively:

- `main` — branch tag (moving)
- `<sha7>` — short commit sha
- `YYYY-MM-DD` — date tag
- `YYYY-MM-DD-<sha7>` — immutable pin tag (use this to pin a deploy)
- `latest`

Deployed in the homelab via GitOps; the image is pinned by tag + digest in
`homelab/apps/base/modemscope/deployment.yaml`. Bump the pin there — do not
repoint `latest`.

## Conventions

- All changes go through a branch and a pull request; never commit directly to
  `main`. Get the PR reviewed and let CI pass before merge.
