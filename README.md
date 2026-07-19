# blenau CLI

Agent-first command-line interface for [Blenau](https://github.com/getblenau),
written in Go. Full docs: **https://docs.blenau.com/cli/**

## Install

Download the binary for your platform from the
[latest release](https://github.com/getblenau/cli/releases/latest) (macOS Intel
& Apple Silicon, Linux amd64 & arm64, Windows amd64), extract it, and put
`blenau` on your `PATH`.

Or with Go:

```sh
go install github.com/getblenau/cli@latest
```

(`go install` names the binary after the module path; rename it to `blenau`. The
release archives are already named `blenau`.)

## Log in

The CLI supports **both** auth paths:

```sh
blenau login                      # browser login — most secure for a person;
                                  # refresh token stored in the OS keychain
blenau login --token blenau_tk_…  # service token — best for CI/automation
```

You can also set `BLENAU_API_TOKEN` (and `BLENAU_API_URL`) via the environment
for unattended use. See
[Which authentication should I use?](https://docs.blenau.com/cli/).

## Design principles

- **Agent-first.** Every command supports `--json` for structured output.
  `--help` is exhaustive. `--agent-manifest` emits the full JSON contract of the
  CLI (name, version, commands, flags) so agents can discover the surface
  without scraping help text.
- **UTF-8 strict.** Stdout is always UTF-8 NFC, no BOM. Behavior is identical on
  Linux, macOS, and Windows (we force code page 65001 on Windows). Encoding is
  exercised by tests.
- **HTTP client only.** No local business logic. The CLI talks to the Blenau
  HTTP API ([`getblenau/api`](https://github.com/getblenau/api)) and is
  responsible only for auth, request formatting, and response rendering.

## Build

```sh
go mod tidy
go build ./...
go test ./...
```

## Releases

Cross-platform binaries are produced by [GoReleaser](https://goreleaser.com)
(see `.goreleaser.yml`) on each `vX.Y.Z` tag and published to
[GitHub Releases](https://github.com/getblenau/cli/releases). Targets:
linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64.
