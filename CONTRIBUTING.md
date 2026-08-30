# Contributing

## Development setup

Go 1.27+ and [ExifTool](https://exiftool.org/) on `PATH`; integration
tests skip without ExifTool, but CI always runs them.

```console
$ go build ./...
$ go test -race ./...
$ golangci-lint run
```

All three must pass.

## Expectations

- Every change ships with tests and any needed documentation in the
  same commit. End-to-end tests build real archives in temporary
  directories and go through the public CLI.
- Safety invariants are not configurable: never overwrite, never
  delete a source before its copy is verified, never produce a name
  from a partial date, never rename a write-once file whose content
  disagrees with its name. Treat a change that would weaken one as a
  design discussion, not a patch.
- Conventional Commits (`feat:`, `fix:`, `docs:`, `ci:`, `chore:`).
- Naming: Stampla in prose; `stampla` for the command, the module and
  all identifiers — never two words.
- Dependencies: the standard library and `golang.org/x/*` only.
  Anything further is a design discussion.

## Licensing

Stampla is GPL-3.0-or-later. External contributions require a
contributor license agreement (a non-exclusive, Apache-style CLA)
before merge; it keeps the project able to offer the code under
additional terms later, and it is asked for transparently on a first
pull request.

## Releasing

Releases are deliberate and manual; automation takes over at the tag:

1. Retitle the changelog's `[Unreleased]` section to `[X.Y.Z] - date`.
2. Commit as `chore: release X.Y.Z`, tag `vX.Y.Z`, push both.

The tag triggers `release.yml`: full checks, then goreleaser builds
the platform binaries and creates the GitHub release. The version
string is stamped at build time from the tag.
