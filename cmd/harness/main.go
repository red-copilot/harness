package main

import (
	"os"

	"github.com/red-copilot/harness/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
