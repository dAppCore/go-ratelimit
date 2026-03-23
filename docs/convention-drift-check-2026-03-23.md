<!-- SPDX-License-Identifier: EUPL-1.2 -->

# Convention Drift Check

Date: 2026-03-23

`CODEX.md` was not present anywhere under `/workspace`, so this check used
`CLAUDE.md`, `docs/development.md`, and the current tree.

## stdlib -> core.*

- `CLAUDE.md:65` still documents `forge.lthn.ai/core/go-io`, while the direct
  dependency in `go.mod:6` is `dappco.re/go/core/io`.
- `CLAUDE.md:66` still documents `forge.lthn.ai/core/go-log`, while the direct
  dependency in `go.mod:7` is `dappco.re/go/core/log`.
- `docs/development.md:175` still mandates `fmt.Errorf("ratelimit.Function: what: %w", err)`,
  but the implementation and repo guidance use `coreerr.E(...)`
  (`ratelimit.go:19`, `sqlite.go:8`, `CLAUDE.md:31`).
- `docs/development.md:195` omits the `core.*` direct dependencies entirely;
  `go.mod:6-7` now declares both `dappco.re/go/core/io` and
  `dappco.re/go/core/log`.
- `docs/index.md:102` likewise omits the `core.*` direct dependencies that are
  present in `go.mod:6-7`.

## UK English

- `README.md:2` uses `License` in the badge alt text.
- `CONTRIBUTING.md:34` uses `License` as the section heading.

## Missing Tests

- `docs/development.md:150` says current coverage is `95.1%` and the floor is
  `95%`, but `go test -coverprofile=/tmp/convention-cover.out ./...` currently
  reports `94.4%`.
- `docs/history.md:28` still records coverage as `95.1%`; the current tree is
  below that figure at `94.4%`.
- `docs/history.md:66` and `docs/development.md:157` say the
  `os.UserHomeDir()` branch in `NewWithConfig()` is untestable, but
  `error_test.go:648` now exercises that path.
- `ratelimit.go:202` (`Load`) is only `90.0%` covered.
- `ratelimit.go:242` (`Persist`) is only `90.0%` covered.
- `ratelimit.go:650` (`CountTokens`) is only `71.4%` covered.
- `sqlite.go:20` (`newSQLiteStore`) is only `64.3%` covered.
- `sqlite.go:110` (`loadQuotas`) is only `92.9%` covered.
- `sqlite.go:194` (`loadState`) is only `88.6%` covered.
- `ratelimit_test.go:746` starts a mock server for `CountTokens`, but the test
  contains no assertion that exercises the success path.
- `iter_test.go:108` starts a second mock server for `CountTokens`, but again
  does not exercise the mocked path.
- `error_test.go:42` defines `TestSQLiteInitErrors`, but the `WAL pragma failure`
  subtest is still an empty placeholder.

## SPDX Headers

- `CLAUDE.md:1` is missing an SPDX header.
- `CONTRIBUTING.md:1` is missing an SPDX header.
- `README.md:1` is missing an SPDX header.
- `docs/architecture.md:1` is missing an SPDX header.
- `docs/development.md:1` is missing an SPDX header.
- `docs/history.md:1` is missing an SPDX header.
- `docs/index.md:1` is missing an SPDX header.
- `error_test.go:1` is missing an SPDX header.
- `go.mod:1` is missing an SPDX header.
- `iter_test.go:1` is missing an SPDX header.
- `ratelimit.go:1` is missing an SPDX header.
- `ratelimit_test.go:1` is missing an SPDX header.
- `sqlite.go:1` is missing an SPDX header.
- `sqlite_test.go:1` is missing an SPDX header.

## Verification

- `go test ./...`
- `go test -coverprofile=/tmp/convention-cover.out ./...`
- `go test -race ./...`
- `go vet ./...`
