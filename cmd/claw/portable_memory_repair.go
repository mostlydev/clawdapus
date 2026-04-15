package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const portableMemoryRepairImage = "busybox:1.36.1"

var errPortableMemoryRepairNeeded = errors.New("portable memory repair needed")

func portableMemoryTargetMode(info os.FileInfo) (os.FileMode, bool) {
	switch {
	case info.IsDir():
		return 0o777, true
	case info.Mode().IsRegular():
		return 0o666, true
	default:
		return 0, false
	}
}

func portableMemoryNeedsRepair(memoryDir string) (bool, string, error) {
	_, err := os.Stat(memoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("stat portable memory %q: %w", memoryDir, err)
	}
	targetUID, targetGID, haveOwner := currentPortableMemoryRepairOwner()

	var reason string
	err = filepath.Walk(memoryDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walk portable memory %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		targetMode, ok := portableMemoryTargetMode(info)
		if !ok {
			return nil
		}
		if info.Mode().Perm() != targetMode {
			reason = fmt.Sprintf("%s mode=%o want=%o", path, info.Mode().Perm(), targetMode)
			return errPortableMemoryRepairNeeded
		}
		if haveOwner {
			uid, gid, ok := portableMemoryFileOwner(info)
			if ok && (uid != targetUID || gid != targetGID) {
				reason = fmt.Sprintf("%s owner=%d:%d want=%d:%d", path, uid, gid, targetUID, targetGID)
				return errPortableMemoryRepairNeeded
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errPortableMemoryRepairNeeded) {
			return true, reason, nil
		}
		return false, "", err
	}
	return false, "", nil
}

func repairPortableMemoryWithDocker(memoryDir string) error {
	absPath, err := filepath.Abs(memoryDir)
	if err != nil {
		return fmt.Errorf("resolve portable memory path %q: %w", memoryDir, err)
	}

	args := []string{
		"run", "--rm",
		"--user", "0:0",
		"-v", absPath + ":/portable-memory:rw",
	}
	if uid, gid, ok := currentPortableMemoryRepairOwner(); ok {
		args = append(args,
			"-e", fmt.Sprintf("HOST_UID=%d", uid),
			"-e", fmt.Sprintf("HOST_GID=%d", gid),
		)
	}
	args = append(args,
		portableMemoryRepairImage,
		"sh", "-ceu",
		`find /portable-memory -xdev -type d -exec chmod 0777 {} \;
find /portable-memory -xdev -type f -exec chmod 0666 {} \;
if [ -n "${HOST_UID:-}" ] && [ -n "${HOST_GID:-}" ]; then
	find /portable-memory -xdev -type d -exec chown "${HOST_UID}:${HOST_GID}" {} \;
	find /portable-memory -xdev -type f -exec chown "${HOST_UID}:${HOST_GID}" {} \;
fi`,
	)
	if err := runInfraDockerCommand(args...); err != nil {
		return fmt.Errorf("docker helper repair: %w", err)
	}
	return nil
}
