//go:build unix

package cli

import (
	"os"
	"syscall"
)

// sameDevice reports whether two paths sit on the same filesystem.
//
// It is the question "is anything mounted here", asked the way the
// kernel answers it. A path that cannot be stat'd reads as the same
// device, so a walk looking for a mount point keeps climbing rather than
// declaring one it cannot see. The two device numbers are compared as
// the platform declares them: the type differs between Unixes, and
// converting it to a common one is a conversion with nothing to gain.
func sameDevice(a, b string) bool {
	first, ok := deviceOf(a)
	if !ok {
		return true
	}
	second, ok := deviceOf(b)
	if !ok {
		return true
	}
	return first.Dev == second.Dev
}

func deviceOf(path string) (*syscall.Stat_t, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return stat, ok
}
