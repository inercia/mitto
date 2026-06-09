package web

import (
	"fmt"

	"github.com/shirou/gopsutil/v4/process"
)

// processTreeRSS returns the total resident set size (RSS) in bytes summed over
// the process tree rooted at pid — the root process plus all descendants. A
// shared ACP agent typically spawns a child tree (e.g. node → claude), so the
// total memory footprint requires walking the whole tree.
//
// Per-process errors during the walk are tolerated by skipping that process (a
// child may exit mid-walk). Only a failure to look up the ROOT process is
// returned as an error.
func processTreeRSS(pid int) (uint64, error) {
	root, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, fmt.Errorf("lookup root process %d: %w", pid, err)
	}

	var total uint64
	if mi, err := root.MemoryInfo(); err == nil && mi != nil {
		total += mi.RSS
	}
	total += descendantsRSS(root)
	return total, nil
}

// descendantsRSS recursively sums the RSS of all descendants of p. Per-process
// errors are skipped so a child exiting mid-walk does not fail the whole sum.
func descendantsRSS(p *process.Process) uint64 {
	children, err := p.Children()
	if err != nil {
		return 0
	}
	var total uint64
	for _, child := range children {
		if mi, err := child.MemoryInfo(); err == nil && mi != nil {
			total += mi.RSS
		}
		total += descendantsRSS(child)
	}
	return total
}
