//go:build windows

package cli

// sameDevice always reports one filesystem on Windows.
//
// Nothing calls it with a path that could be on removable media —
// removablePrefix answers false for every Windows path, because a card
// reader there is an ordinary drive letter — so this exists to keep the
// mount-point walk compiling, and it ends that walk at the drive root.
func sameDevice(_, _ string) bool { return true }
