// SPDX-License-Identifier: EUPL-1.2

module dappco.re/go/ratelimit

go 1.26.0

require (
	dappco.re/go v0.9.0
	gopkg.in/yaml.v3 v3.0.1 // Note: YAML parse for rate limit rules config; no core.* YAML parser.
	modernc.org/sqlite v1.47.0 // Note: pure-Go SQLite for rate limit state persistence; no core.* SQLite driver.
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/tools v0.43.0 // indirect
	modernc.org/libc v1.70.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
