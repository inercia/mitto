package acpproc

import (
	"errors"
	"os"
	"testing"
)

func TestSampleProcessTreeMemory_CurrentProcess(t *testing.T) {
	sample, err := sampleProcessTreeMemory(os.Getpid())
	if err != nil {
		t.Fatalf("sample current process tree: %v", err)
	}
	if sample.parentRSS == 0 {
		t.Fatal("current process parent RSS is zero")
	}
	if minimum := sample.parentRSS + sample.descendantRSS; sample.effectiveMemory < minimum {
		t.Fatalf("effective memory = %d, want at least aggregate RSS %d", sample.effectiveMemory, minimum)
	}
}

// TestMemorySampleFromTopology_MITTO6Y69 proves the total and diagnostic
// breakdown are derived from the same captured topology.
func TestMemorySampleFromTopology_MITTO6Y69(t *testing.T) {
	topology := []processTopologyEntry{
		{pid: 10, parentPID: 1},
		{pid: 11, parentPID: 10},
		{pid: 20, parentPID: 2}, // Unrelated tree.
	}
	rssValues := map[int32]uint64{1: 100, 10: 20, 11: 30, 20: 1000}
	footprintValues := map[int32]uint64{1: 90, 10: 200, 11: 10, 20: 1000}
	rssCalls := make(map[int32]int)
	footprintCalls := make(map[int32]int)

	sample := memorySampleFromTopology(
		1,
		topology,
		func(pid int32) (uint64, error) {
			rssCalls[pid]++
			return rssValues[pid], nil
		},
		func(pid int32) (uint64, error) {
			footprintCalls[pid]++
			return footprintValues[pid], nil
		},
	)

	if sample.effectiveMemory != 300 || sample.parentRSS != 100 ||
		sample.descendantRSS != 50 || sample.descendantCount != 2 {
		t.Fatalf("unexpected unified sample: %+v", sample)
	}
	for _, pid := range []int32{1, 10, 11} {
		if rssCalls[pid] != 1 || footprintCalls[pid] != 1 {
			t.Errorf("pid %d sampled rss=%d footprint=%d times; want once each", pid, rssCalls[pid], footprintCalls[pid])
		}
	}
	if rssCalls[20] != 0 || footprintCalls[20] != 0 {
		t.Fatal("unrelated process tree was sampled")
	}
}

func TestMemorySampleFromTopology_FallsBackToRSS(t *testing.T) {
	topology := []processTopologyEntry{{pid: 10, parentPID: 1}}
	footprintCalls := 0
	sample := memorySampleFromTopology(
		1,
		topology,
		func(pid int32) (uint64, error) {
			if pid == 1 {
				return 100, nil
			}
			return 50, nil
		},
		func(int32) (uint64, error) {
			footprintCalls++
			return 0, errors.New("unsupported")
		},
	)

	if sample.effectiveMemory != 150 {
		t.Fatalf("effective memory = %d, want RSS fallback 150", sample.effectiveMemory)
	}
	if footprintCalls != 1 {
		t.Fatalf("footprint reader called %d times; want only the root capability probe", footprintCalls)
	}
}

func TestMemorySampleFromTopology_SkipsExitedChildFootprint(t *testing.T) {
	topology := []processTopologyEntry{{pid: 10, parentPID: 1}, {pid: 11, parentPID: 1}}
	sample := memorySampleFromTopology(
		1,
		topology,
		func(int32) (uint64, error) { return 50, nil },
		func(pid int32) (uint64, error) {
			if pid == 10 {
				return 0, errors.New("process exited")
			}
			return 40, nil
		},
	)

	if sample.effectiveMemory != 150 {
		t.Fatalf("effective memory = %d, want RSS 150 after child footprint failure", sample.effectiveMemory)
	}
}
