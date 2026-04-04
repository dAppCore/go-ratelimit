<!-- SPDX-License-Identifier: EUPL-1.2 -->

# Contributing

Thank you for your interest in contributing!

## Requirements
- **Go Version**: 1.26 or higher is required.
- **Tools**: `golangci-lint` is recommended.

## Development Workflow
1. **Testing**: Ensure all tests pass before submitting changes.
   ```bash
   go build ./...
   go test ./...
   go test -race ./...
   go test -cover ./...
   go mod tidy
   ```
2. **Code Style**: All code must follow standard Go formatting.
   ```bash
   gofmt -w .
   go vet ./...
   ```
3. **Linting**: We use `golangci-lint` to maintain code quality.
   ```bash
   golangci-lint run ./...
   ```

## Commit Message Format
We follow the [Conventional Commits](https://www.conventionalcommits.org/) specification using the repository format `type(scope): description`:
- `feat`: A new feature
- `fix`: A bug fix
- `docs`: Documentation changes
- `refactor`: A code change that neither fixes a bug nor adds a feature
- `chore`: Changes to the build process or auxiliary tools and libraries

Common scopes: `ratelimit`, `sqlite`, `persist`, `config`

Example:

```text
fix(ratelimit): align module metadata with dappco.re

Co-Authored-By: Virgil <virgil@lethean.io>
```

## Licence
By contributing to this project, you agree that your contributions will be licensed under the **European Union Public Licence (EUPL-1.2)**.
