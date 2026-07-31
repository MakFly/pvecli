// Command pvectl administers a Proxmox VE node through its REST API.
package main

import (
	"errors"
	"os"

	"github.com/MakFly/pvectl/cmd"
)

// exitCoder is implemented by errors that know which exit code of PRD §7.5
// they deserve. Declared here rather than imported: a structural interface
// keeps main from depending on whichever package produced the error.
type exitCoder interface {
	ExitCode() int
}

// Build metadata, injected at link time (see the Makefile):
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc1234"
//
// These two values describe *this binary* and nothing else. The version of the
// PVE node is a completely different thing, fetched over the API by the
// `version` subcommand — see cmd/version.go.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := cmd.Execute(version, commit); err != nil {
		// Cobra has already printed the error. A script reading $? still needs
		// to tell "wrong token" from "node said no" — hence the typed codes.
		// The table is completed by PVX-007 (2 usage, 4 task, 5 refused).
		var coded exitCoder
		if errors.As(err, &coded) {
			os.Exit(coded.ExitCode())
		}
		os.Exit(1)
	}
}
