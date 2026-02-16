package acp

import (
	"fmt"

	"github.com/google/shlex"
)

// ParseCommand parses an ACP command string into arguments using shell-aware tokenization.
// It handles quoted strings correctly, for example:
//   - "sh -c 'cd /dir && cmd'" -> ["sh", "-c", "cd /dir && cmd"]
//   - "auggie --profile \"my profile\"" -> ["auggie", "--profile", "my profile"]
//
// Returns an error if the command string has invalid quoting (e.g., unclosed quotes)
// or if the command is empty.
func ParseCommand(command string) ([]string, error) {
	args, err := shlex.Split(command)
	if err != nil {
		return nil, fmt.Errorf("failed to parse command %q: %w", command, err)
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return args, nil
}
