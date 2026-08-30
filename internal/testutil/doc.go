// Package testutil carries the fixtures and helpers the integration
// tests share: committed sample media, a way to give a copy of it any
// capture time, and the small amount of ExifTool driving a test needs to
// read back what stampla did.
//
// It is a test dependency only. Nothing outside a _test.go file imports
// it, and it imports no other stampla package: a helper built out of the
// code under test would let a bug in that code decide whether the tests
// that catch it run at all.
//
// # Fixtures
//
// testdata holds synthetic generated media — 16x16 black video frames
// and a 16x16 gray JPEG — described in testdata/README.md. Fixture reads
// one; CopyFixture writes a copy somewhere a test may mutate it. Nothing
// here hands out a path into testdata, because a committed fixture that
// a test can rewrite is a fixture that will eventually be rewritten.
//
// The committed capture times and image-data hashes are also constants
// (JPEGDate, JPEGHash, …), so a test can state the canonical name it
// expects rather than recomputing it.
//
// # Capture times
//
// StampJPEG, StampVideo and WriteSidecar each put a copy of a fixture at
// a path with the capture time the caller asked for, so a test needing
// twenty files with twenty distinct times needs no new binary fixtures.
// All three accept ExifTool's own date form, "2026:07:03 15:07:27".
//
// # ExifTool
//
// RequireExifTool skips a test when ExifTool is not installed, and the
// helpers that need it call it themselves, so a test that only uses this
// package is gated without saying so. An ExifTool that is installed but
// unusable is never a skip: it fails, loudly, wherever it is used.
package testutil
