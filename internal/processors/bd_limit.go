package processors

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/inercia/mitto/internal/bdexec"
)

// runProcessorCommand routes command processors that invoke bd through the
// same process-wide gate as the typed beads client and CEL helpers.
func runProcessorCommand(ctx context.Context, proc *Processor, cmd *exec.Cmd) error {
	if !processorInvokesBD(proc) {
		return cmd.Run()
	}
	release, err := bdexec.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return cmd.Run()
}

func processorInvokesBD(proc *Processor) bool {
	if filepath.Base(proc.ResolveCommand()) == "bd" {
		return true
	}
	for _, arg := range proc.Args {
		for _, token := range strings.FieldsFunc(arg, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_./-", r)
		}) {
			if filepath.Base(token) == "bd" {
				return true
			}
		}
	}
	return false
}
