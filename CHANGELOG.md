# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the
project uses [Semantic Versioning](https://semver.org/).

This is the Go implementation's changelog, starting fresh at v0.1.0.
The Python implementation's history lives in
[stampla-python](https://github.com/stampla/stampla-python/blob/main/CHANGELOG.md).

## [Unreleased]

### Added

- The convergence engine: one operation placing files at their
  identity names (`YYYYMMDD_HHMMSS_hhhhhhhh.ext`, capture time plus
  ImageDataHash slice) under a destination root.
- `stampla cp` / `stampla mv` — converge by copying or moving;
  `mv` verifies every copy at its destination before deleting a
  source, and heals an archive in place.
- `stampla verify` — membership check (`verify <src> <dest>`, exit 0
  means every source file is accounted for in the archive) and
  archive self-check (`verify <dest>`), descending into nested roots.
- `.stampla` destination markers: per-archive `layout`, container
  `layout-for-children` inheritance, `dam` protection; automatic
  marker writing after successful mutations; layout provenance in
  every report.
- Plain-text receipts (`.stampla.log`) recording every applied
  mutation.
- Deterministic confirmation tripwires (layout mismatch, mass
  relocation, DAM artifacts, removable-media moves) with `-y`.
- Corruption refusal: write-once formats are never renamed on
  identity mismatch; distinct `corrupt` and `time-drift` alarms.
- `--porcelain` NDJSON output (`format: 1`), `--stdin -z` input,
  dry-run previews (`-n`), diff-style exit codes, `--color` with
  `NO_COLOR` support, `--workers`, and `stampla version` / `help`.
