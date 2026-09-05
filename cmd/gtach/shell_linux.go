package main

import (
	"fmt"
	"os"
)

func parentExecutable(pid int) string {
	path, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	return path
}
