// Command vendorapp-fake is a test double for the vendor app CLI, used by
// the Tier-1 integration tests (no vendor binary, no scanner). Point the
// tools at it with `--native --native-path /path/to/vendorapp-fake`.
// See internal/fakescan for behaviour and test hooks.
package main

import (
	"fmt"
	"os"

	fakescan "github.com/scanSTATION/lib-v370/vendorapp/fake"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "vendorapp-fake:", err)
		os.Exit(1)
	}
	os.Exit(fakescan.Run(os.Args[1:], fakescan.Env{
		Getenv: os.Getenv, Home: home, Stdout: os.Stdout, Stderr: os.Stderr,
	}))
}
