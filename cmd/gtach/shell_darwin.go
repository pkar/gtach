package main

import (
	"bytes"

	"golang.org/x/sys/unix"
)

func parentExecutable(pid int) string {
	// KERN_PROCARGS2 starts with argc (int32), then the executable path and NUL.
	// Only use the path; do not retain or log arguments or environment bytes.
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) <= 4 {
		return ""
	}
	end := bytes.IndexByte(raw[4:], 0)
	if end < 0 {
		return ""
	}
	return string(raw[4 : 4+end])
}
