# lalabase

Lalabase from the terminal: one binary for a person with arrow keys (a
full-screen interface) and for scripts and Claude Code (plain text or JSON).

**Status:** foundation (slice C0). Nothing to use day to day yet.

## Build

```sh
go build ./cmd/lalabase
./lalabase version
```

Go version: see `go.mod`.

## Development

```sh
go vet ./...
go test -race ./...
golangci-lint run
go-licenses check ./cmd/... --allowed_licenses=MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,0BSD
```

* **Foreign text** (titles, comments, names, server error texts) goes through
  `internal/clean` before it reaches the terminal, in every mode. Its test
  cases are those of `bin/lala` in the core repository, byte for byte, plus a
  test over every code point. The output is valid UTF-8 for a **UTF-8
  terminal**; an 8-bit terminal that reads C1 controls from single bytes cannot
  be protected by cleaning (ordinary letters carry such bytes once encoded), so
  the output layer refuses a locale that is not UTF-8.
* **No invisible characters in the repository:** `internal/sourceguard` reads
  every tracked text file. Write such characters as escapes or numbers.
* **The server contract** is a copy of `spec/fixtures/mcp_contract.json` of the
  core repository, with its origin in `internal/contract/testdata/SOURCE.json`.
  Refresh it when the client adopts a newer server:
  `go run ./tools/update-contract -core <path to the core checkout> -ref origin/main`.
* **Dependencies** must be permissive (MIT, Apache-2.0, BSD, ISC); CI checks it.

## Release

Push a tag `v*` (a version: `v` plus digits, letters, `.`, `+`, `-`). The
release workflow runs the whole CI on it first, then builds macOS (arm64,
amd64) and Linux (amd64, arm64) without write access, and only the last job
attaches the archives with `checksums.txt` to a GitHub release.

## License

MIT, see `LICENSE`.
