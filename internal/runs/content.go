package runs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

var snapshotIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ContentSelection records the reference version verified at admission.
// The trusted publisher must retain it unchanged while the run is unfinished.
type ContentSelection struct {
	Target         string `json:"target"`
	Snapshot       string `json:"snapshot"`
	ManifestDigest string `json:"manifest_digest"`
}

func checkContentGrant(tpl *Template, spec *Spec) error {
	if tpl.Worker.Home == nil && spec.Content != nil {
		return apiErr(http.StatusBadRequest, "spec_invalid", "content requires a task-home template")
	}
	targets := map[string]bool{}
	for _, c := range tpl.Content {
		if c.Versioned {
			targets[c.Target] = true
		}
	}
	for target, id := range spec.Content {
		if !targets[target] || !snapshotIDRe.MatchString(id) {
			return apiErr(http.StatusBadRequest, "spec_invalid", "content must select a declared versioned target with a valid snapshot ID")
		}
	}
	for target := range targets {
		if _, ok := spec.Content[target]; !ok {
			return apiErr(http.StatusBadRequest, "content_selection_missing", "every versioned content target requires a selection")
		}
	}
	return nil
}

func resolveContentSelections(tpl *Template, spec *Spec) ([]ContentSelection, error) {
	if err := checkContentGrant(tpl, spec); err != nil {
		return nil, err
	}
	var out []ContentSelection
	for _, c := range tpl.Content {
		if !c.Versioned {
			continue
		}
		id := spec.Content[c.Target]
		digest, err := snapshotDigest(c.Source, id)
		if err != nil {
			return nil, err
		}
		out = append(out, ContentSelection{c.Target, id, digest})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out, nil
}

func verifyContentSelections(tpl *Template, rec *Record) error {
	current, err := resolveContentSelections(tpl, rec.Spec)
	if err != nil {
		return err
	}
	if len(current) != len(rec.Content) {
		return fmt.Errorf("content selection changed")
	}
	for i := range current {
		if current[i] != rec.Content[i] {
			return fmt.Errorf("content snapshot changed")
		}
	}
	return nil
}

const (
	maxSnapshotEntries = 20000
	maxSnapshotDepth   = 32
	maxSnapshotBytes   = int64(256 << 20)
)

type snapshotEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

type snapshotWalk struct {
	entries []snapshotEntry
	bytes   int64
}

func invalidSnapshot(rule string) error {
	return apiErr(http.StatusUnprocessableEntity, "content_snapshot_invalid", "content snapshot %s", rule)
}

// snapshotDigest never follows links or opens a device/FIFO for reading.
// Verification is evidence about these descriptors, not a lock on Docker's
// subsequent path lookup. The host publisher owns immutability and retention.
func snapshotDigest(root, id string) (string, error) {
	if !snapshotIDRe.MatchString(id) {
		return "", invalidSnapshot("has an invalid ID")
	}
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open(root, flags, 0)
	if err != nil {
		return "", invalidSnapshot("root is unavailable")
	}
	defer unix.Close(fd)
	child, err := unix.Openat(fd, id, flags, 0)
	if errors.Is(err, unix.ENOENT) {
		return "", apiErr(http.StatusUnprocessableEntity, "content_snapshot_unknown", "selected content snapshot does not exist")
	}
	if err != nil {
		return "", invalidSnapshot("must be a real readable directory")
	}
	f := os.NewFile(uintptr(child), id)
	defer f.Close()
	w := snapshotWalk{}
	if err := w.walk(f, "", 0); err != nil {
		return "", err
	}
	sort.Slice(w.entries, func(i, j int) bool { return w.entries[i].Path < w.entries[j].Path })
	raw, err := json.Marshal(w.entries)
	if err != nil {
		return "", invalidSnapshot("manifest cannot be encoded")
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (w *snapshotWalk) walk(f *os.File, rel string, depth int) error {
	if depth > maxSnapshotDepth || len(w.entries) >= maxSnapshotEntries {
		return invalidSnapshot("exceeds the entry or depth limit")
	}
	info, err := f.Stat()
	if err != nil {
		return invalidSnapshot("cannot be inspected")
	}
	if info.Mode().Perm()&0222 != 0 {
		return invalidSnapshot("contains writable entries; seal it before launch")
	}
	entry := snapshotEntry{Path: rel, Mode: uint32(info.Mode().Perm())}
	switch {
	case info.IsDir():
		if info.Mode().Perm()&0005 != 0005 {
			return invalidSnapshot("directory must be readable and searchable by the worker (o+rx)")
		}
		entry.Kind = "d"
		w.entries = append(w.entries, entry)
		for {
			names, err := f.Readdirnames(128)
			if err != nil && err != io.EOF {
				return invalidSnapshot("directory cannot be read")
			}
			for _, name := range names {
				if !utf8.ValidString(name) {
					return invalidSnapshot("contains a non-UTF-8 name")
				}
				if len(w.entries) >= maxSnapshotEntries {
					return invalidSnapshot("exceeds the entry limit")
				}
				var st unix.Stat_t
				if unix.Fstatat(int(f.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
					return invalidSnapshot("entry cannot be inspected")
				}
				kind := st.Mode & unix.S_IFMT
				if kind != unix.S_IFDIR && kind != unix.S_IFREG {
					return invalidSnapshot("contains a link or special file")
				}
				flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
				if kind == unix.S_IFDIR {
					flags |= unix.O_DIRECTORY
				}
				fd, e := unix.Openat(int(f.Fd()), name, flags, 0)
				if e != nil {
					return invalidSnapshot("entry cannot be opened without following links")
				}
				child := os.NewFile(uintptr(fd), name)
				path := name
				if rel != "" {
					path = rel + "/" + name
				}
				e = w.walk(child, path, depth+1)
				child.Close()
				if e != nil {
					return e
				}
			}
			if err == io.EOF {
				break
			}
		}
	case info.Mode().IsRegular():
		if info.Mode().Perm()&0004 == 0 {
			return invalidSnapshot("file must be readable by the worker (o+r)")
		}
		entry.Kind = "f"
		entry.Size = info.Size()
		if entry.Size < 0 || entry.Size > maxSnapshotBytes-w.bytes {
			return invalidSnapshot("exceeds the byte limit")
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, maxSnapshotBytes-w.bytes+1))
		after, e := f.Stat()
		if err != nil || e != nil || n != entry.Size || after.Size() != entry.Size || after.ModTime() != info.ModTime() || after.Mode() != info.Mode() {
			return invalidSnapshot("changed while being read")
		}
		w.bytes += n
		entry.SHA256 = hex.EncodeToString(h.Sum(nil))
		w.entries = append(w.entries, entry)
	default:
		return invalidSnapshot("contains a link or special file")
	}
	return nil
}
