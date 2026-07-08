//go:build darwin

package coldstart

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/unix"
)

// hostByteOrder returns the platform's native byte order.
func hostByteOrder() binary.ByteOrder {
	var i uint16 = 1
	b := (*[2]byte)(unsafe.Pointer(&i))
	if b[0] == 1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

// readLoad1 returns the 1-minute load average from vm.loadavg.
//
// The darwin kernel exposes:
//
//	struct loadavg {
//	    fixpt_t ldavg[3];  // uint32
//	    long    fscale;    // 4 or 8 bytes; 8 bytes on arm64/amd64
//	};
//
// Alignment inserts padding between ldavg and fscale on 64-bit builds,
// so the buffer may be 20 or 24 bytes. We only need ldavg[0] and fscale.
func readLoad1() (float64, bool) {
	buf, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(buf) < 16 {
		return 0, false
	}
	bo := hostByteOrder()
	ld0 := bo.Uint32(buf[0:4])

	var fscale uint64
	switch {
	case len(buf) >= 24:
		// 64-bit long, 4 bytes of alignment padding after ldavg[3].
		fscale = bo.Uint64(buf[16:24])
	case len(buf) >= 20:
		// 32-bit long or tightly-packed 64-bit long at offset 12.
		// Prefer the aligned 32-bit interpretation.
		fscale = uint64(bo.Uint32(buf[16:20]))
	default:
		return 0, false
	}
	if fscale == 0 {
		return 0, false
	}
	return float64(ld0) / float64(fscale), true
}
