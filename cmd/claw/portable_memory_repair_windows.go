//go:build windows

package main

import "os"

func currentPortableMemoryRepairOwner() (uid, gid int, ok bool) {
	return 0, 0, false
}

func portableMemoryFileOwner(info os.FileInfo) (uid, gid int, ok bool) {
	return 0, 0, false
}
