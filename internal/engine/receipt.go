package engine

import (
	"os"
	"strings"
	"time"
)

// ReceiptName is the file every applied mutation is recorded in. It
// lives at the archive root beside the marker and travels with the
// files.
const ReceiptName = ".stampla.log"

// The receipt is the permanent record of original filenames — the one
// piece of information a rename would otherwise destroy — and it is
// deliberately plain text, one mutation per line, four tab-separated
// fields:
//
//	2026-07-03T15:07:27+02:00<TAB>mv<TAB>/card/DSC_1234.NEF<TAB>/archive/2026/2026-07/20260703_150727_9b677b64.nef
//
// The time is RFC 3339 in the local zone, because a person reading their
// own archive's history reads it in the time they took the photograph
// in. The verb is cp or mv. Both paths are absolute, so a line means the
// same thing from any working directory, and they are written exactly as
// they are: a filename is quoted back the way it was, never normalized
// into something the user never typed.
type receiptLine struct {
	verb Verb
	old  string
	new  string
}

// receiptVerb is the verb a line records. Finishing an interrupted
// rename is recorded as the mv it completes: what happened to the file,
// from the archive's point of view, is that it moved.
func receiptVerb(verb Verb) string {
	if verb == VerbUnlink {
		return string(VerbMove)
	}
	return string(verb)
}

// appendReceipt adds one group's lines and flushes them.
//
// Appended and fsynced after the group has landed, never before: the
// receipts an interrupted run leaves behind then name exactly the groups
// that completed, so re-reading them is never a way to learn about work
// that did not happen.
func appendReceipt(path string, lines []receiptLine) error {
	if len(lines) == 0 {
		return nil
	}
	stamp := time.Now().Format(time.RFC3339)
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(stamp)
		b.WriteByte('\t')
		b.WriteString(receiptVerb(line.verb))
		b.WriteByte('\t')
		b.WriteString(line.old)
		b.WriteByte('\t')
		b.WriteString(line.new)
		b.WriteByte('\n')
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(b.String()); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}
