# TODO.md -- go-ratelimit

## Phase 1: Generalise Beyond Gemini

- [ ] Hardcoded model quotas are Gemini-specific -- abstract to provider-agnostic config
- [ ] Add quota profiles for OpenAI, Anthropic, and local (Ollama/MLX) backends
- [ ] Make default quotas configurable via YAML or environment variables

## Phase 2: Persistent State

- [ ] Currently stores state in YAML file -- not safe for multi-process access
- [ ] Consider SQLite for concurrent read/write safety (WAL mode)
- [ ] Add state recovery on restart (reload sliding window from persisted data)

## Phase 3: Integration

- [ ] Wire into go-ml backends for automatic rate limiting on inference calls
- [ ] Wire into go-ai facade so all providers share a unified rate limit layer
- [ ] Add metrics export (requests/minute, tokens/minute, rejections) for monitoring

---

## Workflow

1. Virgil in core/go writes tasks here after research
2. This repo's dedicated session picks up tasks in phase order
3. Mark `[x]` when done, note commit hash
