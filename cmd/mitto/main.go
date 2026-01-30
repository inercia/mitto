// Package main is the entry point for the mitto CLI application.
package main

import (
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
