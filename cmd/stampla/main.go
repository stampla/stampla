// Command stampla stamps every photo and video with its own identity:
// when it was captured and what it contains.
//
// See internal/cli for the command surface; this file is the process
// around it and nothing else, so that every part of the interface — the
// exit code included — is exercised by tests without a subprocess.
package main

import (
	"os"

	"github.com/stampla/stampla/internal/cli"
)

// version is stamped at build time from the release tag. A build from
// source says so rather than claiming a release it is not.
var version = "dev"

func main() {
	os.Exit(cli.Run(version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
