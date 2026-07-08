//go:build !darwin && !linux

package coldstart

// readLoad1 is not implemented on this platform.
func readLoad1() (float64, bool) {
	return 0, false
}
