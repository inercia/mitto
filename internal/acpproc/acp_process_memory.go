package acpproc

import (
	"fmt"

	"github.com/shirou/gopsutil/v4/process"
)

// processTreeRSS returns the effective memory pressure in bytes for the process
// tree rooted at pid — the greater of aggregate RSS and aggregate physical
// footprint. On macOS, physical footprint includes compressed memory that RSS
// can omit; this prevents a V8 heap from reaching its limit while Tier 4 still
// sees the process tree below its recycle threshold (mitto-52mt).
//
// Per-process errors during the walk are tolerated by skipping that process (a
// child may exit mid-walk). Only a failure to look up the ROOT process is
// returned as an error.
func processTreeRSS(pid int) (uint64, error) {
	parent, descendants, _, err := processTreeRSSDetailed(pid)
	if err != nil {
		return 0, err
	}
	rss := parent + descendants
	footprint, err := processTreePhysicalFootprint(pid)
	if err != nil {
		return rss, nil // Unsupported or transiently unavailable: retain RSS fallback.
	}
	return effectiveProcessTreeMemory(rss, footprint), nil
}

func effectiveProcessTreeMemory(rss, physicalFootprint uint64) uint64 {
	if physicalFootprint > rss {
		return physicalFootprint
	}
	return rss
}

// processTreePhysicalFootprint sums the platform physical-footprint metric for
// the root process and every descendant. Per-child errors are tolerated because
// children can exit during the walk; a root lookup/sample error causes callers
// to fall back to RSS.
func processTreePhysicalFootprint(pid int) (uint64, error) {
	root, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, fmt.Errorf("lookup root process %d: %w", pid, err)
	}
	rootFootprint, err := processPhysicalFootprint(pid)
	if err != nil {
		return 0, fmt.Errorf("sample root process %d physical footprint: %w", pid, err)
	}
	return rootFootprint + descendantsPhysicalFootprint(root), nil
}

func descendantsPhysicalFootprint(p *process.Process) uint64 {
	children, err := p.Children()
	if err != nil {
		return 0
	}
	var total uint64
	for _, child := range children {
		if footprint, err := processPhysicalFootprint(int(child.Pid)); err == nil {
			total += footprint
		}
		total += descendantsPhysicalFootprint(child)
	}
	return total
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
