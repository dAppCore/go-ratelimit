<!-- SPDX-License-Identifier: EUPL-1.2 -->

[![Go Reference](https://pkg.go.dev/badge/dappco.re/go/core/go-ratelimit.svg)](https://pkg.go.dev/dappco.re/go/core/go-ratelimit)
![Licence: EUPL-1.2](https://img.shields.io/badge/Licence-EUPL--1.2-blue.svg)
[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat&logo=go)](go.mod)

# go-ratelimit

Provider-agnostic sliding window rate limiter for LLM API calls. Enforces requests per minute (RPM), tokens per minute (TPM), and requests per day (RPD) quotas per model using an in-memory sliding window. Ships with default quota profiles for Gemini, OpenAI, Anthropic, and a local inference provider. State persists across process restarts via YAML (single-process) or SQLite (multi-process, WAL mode). Includes a Gemini-specific token counting helper and a YAML-to-SQLite migration path.

**Module**: `dappco.re/go/core/go-ratelimit`
**Licence**: EUPL-1.2
**Language**: Go 1.26

## Quick Start

```go
import "dappco.re/go/core/go-ratelimit"

// YAML backend (default, single-process)
rl, err := ratelimit.New()
if err != nil {
    log.Fatal(err)
}

// SQLite backend (multi-process)
rl, err = ratelimit.NewWithSQLite("/tmp/ratelimits.db")
if err != nil {
    log.Fatal(err)
}
defer rl.Close()

if rl.CanSend("gemini-2.0-flash", 1500) {
    rl.RecordUsage("gemini-2.0-flash", 1000, 500)
}

if err := rl.Persist(); err != nil {
    log.Fatal(err)
}
```

## Documentation

- [Architecture](docs/architecture.md) — sliding window algorithm, provider quotas, YAML and SQLite backends
- [Development Guide](docs/development.md) — prerequisites, test patterns, coding standards
- [Project History](docs/history.md) — completed phases with commit hashes, known limitations

## Build & Test

```bash
go build ./...
go test ./...
go test -race ./...
go vet ./...
go test -cover ./...
go mod tidy
```

## Licence

European Union Public Licence 1.2.
