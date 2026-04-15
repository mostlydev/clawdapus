//go:build !windows

package main

import (
	"os"
	"syscall"
)

func currentPortableMemoryRepairOwner() (uid, gid int, ok bool) {
	return os.Getuid(), os.Getgid(), true
}

func portableMemoryFileOwner(info os.FileInfo) (uid, gid int, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(stat.Uid), int(stat.Gid), true
}
