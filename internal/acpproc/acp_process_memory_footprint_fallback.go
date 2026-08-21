//go:build !darwin || !cgo

package acpproc

import "errors"

func processPhysicalFootprint(int) (uint64, error) {
	return 0, errors.New("physical footprint is unavailable on this platform")
}
