//go:build linux

package coldstart

import (
	"os"
	"strconv"
	"strings"
)

// readLoad1 parses the 1-minute load average from /proc/loadavg.
func readLoad1() (float64, bool) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
