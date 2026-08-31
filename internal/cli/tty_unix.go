//go:build unix

package cli

import (
	"io"
	"os"
)

// controllingTerminal opens the terminal the person is at.
//
// It is deliberately not stdin: stdin may be the --stdin file list, and
// a prompt that read it would consume a path as an answer and then lose
// that path from the run. A session with no controlling terminal — cron,
// a service, a pipeline — fails here, which is what turns an unattended
// unusual run into a refusal instead of a hang.
func controllingTerminal() (io.ReadCloser, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}
