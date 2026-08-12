//go:build darwin && cgo

package acpproc

/*
#include <errno.h>
#include <libproc.h>
#include <sys/resource.h>

static uint64_t mitto_phys_footprint(int pid, int *errnum) {
	struct rusage_info_v2 info = {0};
	if (proc_pid_rusage(pid, RUSAGE_INFO_V2, (rusage_info_t *)&info) != 0) {
		*errnum = errno;
		return 0;
	}
	*errnum = 0;
	return info.ri_phys_footprint;
}
*/
import "C"

import (
	"fmt"
	"syscall"
)

func processPhysicalFootprint(pid int) (uint64, error) {
	var errno C.int
	footprint := uint64(C.mitto_phys_footprint(C.int(pid), &errno))
	if errno != 0 {
		return 0, fmt.Errorf("proc_pid_rusage: %w", syscall.Errno(errno))
	}
	return footprint, nil
}
