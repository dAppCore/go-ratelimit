# go-ratelimit

Provider-agnostic sliding window rate limiter for LLM API calls. Enforces requests per minute (RPM), tokens per minute (TPM), and requests per day (RPD) quotas per model using an in-memory sliding window. Ships with default quota profiles for Gemini, OpenAI, Anthropic, and a local inference provider. State persists across process restarts via YAML (single-process) or SQLite (multi-process, WAL mode). Includes a Gemini-specific token counting helper and a YAML-to-SQLite migration path.

**Module**: `forge.lthn.ai/core/go-ratelimit`
**Licence**: EUPL-1.2
**Language**: Go 1.25

## Quick Start

```go
import "forge.lthn.ai/core/go-ratelimit"

// YAML backend (default, single-process)
rl, err := ratelimit.New()

// SQLite backend (multi-process)
rl, err := ratelimit.NewWithSQLite("~/.core/ratelimits.db")
defer rl.Close()

ok, reason := rl.CanSend("gemini-2.0-flash", 1500)
if ok {
    rl.RecordUsage("gemini-2.0-flash", 1500)
}
```

## Documentation

- [Architecture](docs/architecture.md) — sliding window algorithm, provider quotas, YAML and SQLite backends
- [Development Guide](docs/development.md) — prerequisites, test patterns, coding standards
- [Project History](docs/history.md) — completed phases with commit hashes, known limitations

## Build & Test

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

## Licence

European Union Public Licence 1.2 — see [LICENCE](LICENCE) for details.
