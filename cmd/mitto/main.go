// Package main is the entry point for the mitto CLI application.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		code := 1
		var ec interface{ ExitCode() int }
		if errors.As(err, &ec) {
			code = ec.ExitCode()
		}
		os.Exit(code)
	}
}
