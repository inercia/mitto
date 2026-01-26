// Package main is the entry point for the mitto CLI application.
package main

import (
	"os"

	"github.com/inercia/mitto/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
