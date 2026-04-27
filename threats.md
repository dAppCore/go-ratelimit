## 1. Clock-skew

Status: WIP

Question: Can wall-clock changes bypass or wedge the sliding windows?

Finding: YES, for persisted or externally constructed state. Live in-memory
timestamps produced by `time.Now()` carry Go monotonic readings, so comparisons
between live entries and later `time.Now()` values use monotonic elapsed time.
Persisted YAML/SQLite state and hand-constructed test state lose those monotonic
readings, so `Before`/`After`/`Sub` fall back to wall-clock semantics. A
backwards wall jump could leave request/token entries and `DayStart` in the
future, making prune retain them and making retry times longer than intended.
Severity: Medium. Fixed by injecting a private clock for tests and normalising
future `Requests`, `Tokens`, and `DayStart` to `now` before pruning.
Line citation: `ratelimit.go:120`, `ratelimit.go:338`, `ratelimit.go:350`,
`ratelimit.go:953`, `ratelimit_test.go:1187`.

Finding: PARTIAL, for forward wall jumps after persistence/restart. A large
forward wall jump can make persisted in-window entries appear old and recover
quota early because the limiter stores wall timestamps, not a durable monotonic
clock. In-process entries created by `time.Now()` are materially stronger
because Go monotonic comparisons survive wall-clock changes while the process
runs. Severity: Low/Medium, documented residual risk. The limiter cannot
recover durable monotonic elapsed time after serialisation; this is bounded to
the persisted/external-state path.
Line citation: `ratelimit.go:348`, `ratelimit.go:353`, `ratelimit.go:358`,
`ratelimit.go:363`.

Finding: NO for timezone/DST DayStart anchoring. The implementation uses a
rolling `24*time.Hour` window rather than local calendar midnight, so local
timezone and DST transitions do not define the reset boundary. Severity: Low.
Line citation: `ratelimit.go:362`, `ratelimit.go:649`.

## 2. Integer overflow

Status: WIP

Question: Are safe accumulation helpers reachable on every token accumulation
path?

Finding: YES, one overflow path existed after safe accumulation. `RecordUsage`
uses `safeTokenSum`, snapshots use `totalTokenCount`, and `Decide` compares the
safe current total against the estimate. However, `retryAfterForTokens`
previously added `totalTokenCount(tokens) + estimatedTokens` directly. Because
`totalTokenCount` can saturate to `maxInt`, the subsequent addition could wrap
negative and return no retry delay. Severity: High. Fixed by computing the
deficit with `safeTokenSum(totalTokenCount(tokens), estimatedTokens)`.
Line citation: `ratelimit.go:431`, `ratelimit.go:666`, `ratelimit.go:694`,
`ratelimit.go:916`, `ratelimit.go:924`, `ratelimit.go:969`,
`ratelimit_test.go:1213`.

Finding: YES, `DayCount` increment could overflow. `DayCount` remains `int`
because changing it to `int64` crosses the persistence contract in `sqlite.go`,
which was out of the allowed edit set for this ticket. The immediate overflow
hazard was closed by saturating increments at `maxInt`; the type migration
should be handled as a separate persistence-format change. Severity: Medium.
Line citation: `ratelimit.go:104`, `ratelimit.go:434`, `ratelimit.go:943`,
`ratelimit_test.go:1220`.

Finding: PARTIAL for token slice growth. The slice is per request, not per
token, and `RecordUsage` prunes under the write lock before appending. The
specific 1M TPM x 1000 tokens/request example is therefore about 1000 token
entries/minute, not 60M entries. Residual memory risk remains for configurations
with unlimited RPM and extremely small or zero token counts, because both
request and token event slices can grow with request rate inside the one-minute
window. Severity: Low/Medium residual.
Line citation: `ratelimit.go:99`, `ratelimit.go:100`, `ratelimit.go:423`,
`ratelimit.go:432`, `ratelimit.go:433`.

## 3. Sliding-window races

Status: WIP

Question: Is `prune` ever called under `RLock`, and do background pruning and
requests race?

Finding: NO lock-discipline bug found. All public paths that mutate or prune
state take the write lock: `RecordUsage`, `Stats`, `AllStats`, `Decide`, and
`BackgroundPrune`. `Models` uses `RLock` but only reads maps and does not call
`prune`. Severity: Low.
Line citation: `ratelimit.go:398`, `ratelimit.go:419`, `ratelimit.go:566`,
`ratelimit.go:578`, `ratelimit.go:603`.

Finding: NO long lock hold in `WaitForCapacity`. The blocking loop calls
`Decide`, then sleeps on a timer after `Decide` has returned and released the
lock. It does not hold `mu` while waiting. Severity: Low.
Line citation: `ratelimit.go:440`, `ratelimit.go:446`, `ratelimit.go:456`.

Finding: YES design-level race remains between `Decide` and `RecordUsage`.
`Decide` intentionally does not reserve capacity; callers decide, perform the
API call, then record usage later. Multiple callers can all observe capacity
and later overfill the window when they record. This is a known API semantics
gap rather than a data race, because both methods serialize state access with
the write lock. Severity: Medium residual. A reservation API would be required
to close this without changing caller workflow.
Line citation: `ratelimit.go:601`, `ratelimit.go:603`, `ratelimit.go:419`.
