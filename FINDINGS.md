# FINDINGS.md -- go-ratelimit

## 2026-02-19: Split from core/go (Virgil)

### Origin

Extracted from `forge.lthn.ai/core/go` on 19 Feb 2026.

### Architecture

- Sliding window rate limiter (1-minute window)
- Daily request caps per model
- Token counting via Google `CountTokens` API
- Model-specific quota configuration

### Gemini-Specific Defaults

- `gemini-3-pro-preview`: 150 RPM / 1M TPM / 1000 RPD
- Quotas are currently hardcoded -- needs generalisation (see TODO Phase 1)

### Tests

- 1 test file covering sliding window and quota enforcement
