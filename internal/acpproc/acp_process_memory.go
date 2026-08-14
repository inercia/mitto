package acpproc

import (
	"fmt"

	"github.com/shirou/gopsutil/v4/process"
)

type processMemorySample struct {
	effectiveMemory uint64
	parentRSS       uint64
	descendantRSS   uint64
	descendantCount int
}

type processTopologyEntry struct {
	pid       int32
	parentPID int32
}

type processMetricReader func(pid int32) (uint64, error)

// sampleProcessTreeMemory captures the system PID/PPID topology once, then
// derives effective memory and the diagnostic RSS breakdown from that snapshot.
// This avoids gopsutil.Process.Children on every tree node; on macOS each such
// call enumerates every system process and resolves every PPID (mitto-6y69).
func sampleProcessTreeMemory(pid int) (processMemorySample, error) {
	root, err := process.NewProcess(int32(pid))
	if err != nil {
		return processMemorySample{}, fmt.Errorf("lookup root process %d: %w", pid, err)
	}
	processes, err := process.Processes()
	if err != nil {
		return processMemorySample{}, fmt.Errorf("snapshot process topology: %w", err)
	}

	byPID := make(map[int32]*process.Process, len(processes)+1)
	topology := make([]processTopologyEntry, 0, len(processes))
	byPID[root.Pid] = root
	for _, p := range processes {
		byPID[p.Pid] = p
		parentPID, err := p.Ppid()
		if err != nil {
			continue
		}
		topology = append(topology, processTopologyEntry{pid: p.Pid, parentPID: parentPID})
	}

	rssReader := func(pid int32) (uint64, error) {
		p := byPID[pid]
		if p == nil {
			return 0, fmt.Errorf("process %d missing from topology snapshot", pid)
		}
		info, err := p.MemoryInfo()
		if err != nil || info == nil {
			return 0, err
		}
		return info.RSS, nil
	}
	footprintReader := func(pid int32) (uint64, error) {
		return processPhysicalFootprint(int(pid))
	}
	return memorySampleFromTopology(root.Pid, topology, rssReader, footprintReader), nil
}

func effectiveProcessTreeMemory(rss, physicalFootprint uint64) uint64 {
	if physicalFootprint > rss {
		return physicalFootprint
	}
	return rss
}

func memorySampleFromTopology(
	rootPID int32,
	topology []processTopologyEntry,
	rssReader processMetricReader,
	footprintReader processMetricReader,
) processMemorySample {
	descendants := descendantPIDs(rootPID, topology)
	sample := processMemorySample{descendantCount: len(descendants)}
	if rss, err := rssReader(rootPID); err == nil {
		sample.parentRSS = rss
	}
	for _, pid := range descendants {
		if rss, err := rssReader(pid); err == nil {
			sample.descendantRSS += rss
		}
	}

	var footprint uint64
	if rootFootprint, err := footprintReader(rootPID); err == nil {
		footprint = rootFootprint
		for _, pid := range descendants {
			if childFootprint, err := footprintReader(pid); err == nil {
				footprint += childFootprint
			}
		}
	}
	sample.effectiveMemory = effectiveProcessTreeMemory(sample.parentRSS+sample.descendantRSS, footprint)
	return sample
}

func descendantPIDs(rootPID int32, topology []processTopologyEntry) []int32 {
	children := make(map[int32][]int32)
	for _, entry := range topology {
		children[entry.parentPID] = append(children[entry.parentPID], entry.pid)
	}

	seen := map[int32]bool{rootPID: true}
	stack := append([]int32(nil), children[rootPID]...)
	descendants := make([]int32, 0, len(stack))
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		descendants = append(descendants, pid)
		stack = append(stack, children[pid]...)
	}
	return descendants
}
