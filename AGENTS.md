<!-- SPDX-License-Identifier: EUPL-1.2 -->

# go-ratelimit Agent Notes

This repository follows the core/go v0.9.0 consumer layout.

- The Go module lives under `go/`.
- Use core/go primitives from `dappco.re/go` instead of direct banned stdlib imports where wrappers exist.
- Keep public-symbol tests as `Test<File>_<Symbol>_{Good,Bad,Ugly}` in the matching `<file>_test.go`.
- Keep examples in the matching `<file>_example_test.go`.
- Do not edit `.core/`, `external/`, or `/Users/snider/Code/core/go`.
