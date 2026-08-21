//go:build darwin && cgo

package acpproc

import (
	"os"
	"testing"
)

func TestProcessPhysicalFootprint_CurrentProcess(t *testing.T) {
	got, err := processPhysicalFootprint(os.Getpid())
	if err != nil {
		t.Fatalf("processPhysicalFootprint(current process): %v", err)
	}
	if got == 0 {
		t.Fatal("processPhysicalFootprint(current process) = 0, want non-zero")
	}
}
