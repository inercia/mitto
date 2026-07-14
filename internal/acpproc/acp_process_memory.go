package acpproc

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
	parent, descendants, _, err := processTreeRSSDetailed(pid)
	if err != nil {
		return 0, err
	}
	return parent + descendants, nil
}

// processTreeRSSDetailed returns the RSS breakdown of the process tree rooted
// at pid: the root process's own RSS, the RSS summed over all descendants, and
// the number of descendant processes counted. This is the diagnostic form used
// by the GC's memory-recycle log lines so operators can distinguish agent-side
// (parent) growth from MCP-child (descendant) growth without a live ps probe.
//
// Per-process errors during the walk are tolerated by skipping that process (a
// child may exit mid-walk). Only a failure to look up the ROOT process is
// returned as an error.
func processTreeRSSDetailed(pid int) (parent uint64, descendants uint64, descendantCount int, err error) {
	root, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("lookup root process %d: %w", pid, err)
	}

	if mi, err := root.MemoryInfo(); err == nil && mi != nil {
		parent = mi.RSS
	}
	descendants, descendantCount = descendantsRSSDetailed(root)
	return parent, descendants, descendantCount, nil
}

// descendantsRSS recursively sums the RSS of all descendants of p. Per-process
// errors are skipped so a child exiting mid-walk does not fail the whole sum.
func descendantsRSS(p *process.Process) uint64 {
	total, _ := descendantsRSSDetailed(p)
	return total
}

// descendantsRSSDetailed recursively sums the RSS of all descendants of p and
// counts them. Per-process errors are skipped so a child exiting mid-walk does
// not fail the whole sum.
func descendantsRSSDetailed(p *process.Process) (uint64, int) {
	children, err := p.Children()
	if err != nil {
		return 0, 0
	}
	var total uint64
	var count int
	for _, child := range children {
		count++
		if mi, err := child.MemoryInfo(); err == nil && mi != nil {
			total += mi.RSS
		}
		subTotal, subCount := descendantsRSSDetailed(child)
		total += subTotal
		count += subCount
	}
	return total, count
}
