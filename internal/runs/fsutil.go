package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

const maxStateFileBytes = 8 << 20

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

func validName(name string) bool { return nameRe.MatchString(name) }

// writeFileAtomic writes data with fsync, renames it into place and syncs the
// parent directory, so a crash leaves either the old or the new file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func writeJSONFile(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(raw, '\n'), 0o600)
}

// readRegularFile refuses symlinks, FIFOs and other non-regular files, and
// oversized files. The open itself refuses a final-component symlink and never
// blocks, and the checks run on the opened descriptor, so a swap between check
// and open cannot redirect the read.
func readRegularFile(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%s is a symlink", path)
		}
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return raw, nil
}

func readJSONFile(path string, v any) error {
	raw, err := readRegularFile(path, maxStateFileBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

// EnsurePrivateDir creates path as a 0700 directory, or verifies an existing
// one is a real directory (not a symlink) owned by the current user, and
// tightens its mode to 0700. Durable fences (the run ledger, cllama's
// revocation ledger) must not be writable by other host users.
func EnsurePrivateDir(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must be a directory, not a symlink or file", path)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%s must be owned by uid %d", path, os.Geteuid())
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}
