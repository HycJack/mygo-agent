// MyGo Agent — a Codex-style desktop AI coding agent built with MyGo.
//
// This file is only the entry point: flags, the version injected at
// build time, and starting the app package. Everything else lives in
// internal/app and its sub-packages.
package main

import (
	"fmt"
	"os"

	"mygo-agent/internal/app"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Println("mygo-agent " + version)
			return
		case "--help", "-h":
			fmt.Println("mygo-agent " + version + " — a Codex-style desktop AI coding agent built with MyGo.")
			fmt.Println("Run without arguments to start the app.")
			fmt.Println("  --version   print the version")
			return
		}
	}

	a := app.New(version)
	a.Setup()
	if err := a.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
