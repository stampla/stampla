package engine

import (
	"os"
	"strconv"
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
// same thing from any working directory, and they are never normalized
// into a spelling the user never typed.
//
// A path is written as it is unless it holds something the format could
// not survive — see receiptField.
type receiptLine struct {
	verb Verb
	old  string
	new  string
}

// receiptField renders one path field.
//
// A filename may contain a tab or a newline. Written raw, a tab makes a
// five-field line and a newline shatters one record into two unparsable
// ones — and the run reports success either way, having just destroyed
// the only record of what that file used to be called. Such a field is
// therefore written in Go's quoted string form, which the reader
// recognizes by its leading quote and undoes with strconv.Unquote.
//
// Quoting is the exception, not the rule: an ordinary path is written
// plainly, because a receipt is meant to be read by people first. A
// field is quoted when it holds a tab, a newline or a carriage return —
// the three characters the format itself could not survive — or when it
// begins with a quote, which would otherwise be indistinguishable from a
// quoted field.
//
// A backslash is not a trigger, so a Windows path is written exactly as
// Windows spells it. Nothing is lost by that: a reader's rule is two
// lines either way — a field beginning with a quote goes through
// strconv.Unquote, anything else is the path itself — and a backslash
// inside a quoted field is escaped by strconv.Quote, so neither form can
// be read as the other.
func receiptField(value string) string {
	if strings.ContainsAny(value, "\t\n\r") || strings.HasPrefix(value, `"`) {
		return strconv.Quote(value)
	}
	return value
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
		b.WriteString(receiptField(line.old))
		b.WriteByte('\t')
		b.WriteString(receiptField(line.new))
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
