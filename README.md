# blenau CLI

**Status:** scaffold

Agent-first command-line interface for [Blenau](https://github.com/getblenau), written in Go.

## Design principles

- **Agent-first.** Every command supports `--json` for structured output.
  `--help` is exhaustive. `--agent-manifest` emits the full JSON contract
  of the CLI (name, version, commands, flags) so agents can discover the
  surface without scraping help text.
- **UTF-8 strict.** Stdout is always UTF-8 NFC, no BOM. Behavior is
  identical on Linux, macOS, and Windows (we force code page 65001 on
  Windows). Encoding is exercised by tests.
- **Single binary distribution.** Shipped via GitHub Releases, Homebrew,
  and Scoop. No runtime, no installer, no package manager dependencies.
- **HTTP client only.** No local business logic. The CLI talks to the
  Blenau HTTP API ([`getblenau/api`](https://github.com/getblenau/api))
  and is responsible only for auth, request formatting, and response
  rendering.

## Build

```sh
go mod tidy
go build ./...
```

## Releases

Cross-platform binaries are produced by [GoReleaser](https://goreleaser.com)
(see `.goreleaser.yml`). Targets: linux/amd64, linux/arm64, darwin/amd64,
darwin/arm64, windows/amd64.

Homebrew tap and Scoop bucket integrations will be added in a follow-up.
