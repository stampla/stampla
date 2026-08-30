# Fixtures

Synthetic, generated test media — 16x16 black video frames and a 16x16 gray
JPEG — committed so tests read real files rather than mock metadata.

- `date.mp4`, `date.mov` — QuickTime CreateDate `2026:07:03 13:07:27`.
- `nodate.mp4` — the same clip with its creation date left at zero.
- `dated.jpg` — EXIF DateTimeOriginal and CreateDate `2026:07:03 15:07:27`.
- `plain.jpg` — `dated.jpg` with metadata stripped; same pixels, so the same
  ImageDataHash and the same identity hash slice.
- `dated.xmp` — a sidecar carrying exif:DateTimeOriginal `2026-07-03T15:07:27`.
