//go:build windows

package cli

import (
	"io"
	"os"
)

// controllingTerminal opens the console input buffer, which is what
// Windows calls the terminal the person is at.
//
// It is deliberately not stdin: stdin may be the --stdin file list, and
// a prompt that read it would consume a path as an answer. A process
// with no console attached fails here, which is what turns an unattended
// unusual run into a refusal instead of a hang.
func controllingTerminal() (io.ReadCloser, error) {
	return os.OpenFile("CONIN$", os.O_RDWR, 0)
}
