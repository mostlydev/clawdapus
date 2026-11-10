package runs

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

// A task-home worker leaves files for its requester in deliverables/ at the
// top of its home. The supervisor serves that one directory, flat, and
// nothing else in the home. The worker is untrusted and may change the
// directory at any moment, so every read is made through descriptors: the
// directory is opened relative to the home, each file relative to the
// directory, never following a link and never blocking on a FIFO, and a
// file's size, digest and bytes all come from the one descriptor.

const (
	// DeliverablesDir is the directory, inside a task home, that is served.
	DeliverablesDir = "deliverables"
	// MaxDeliverableBytes bounds one file. Base64 of this fits well inside
	// a 1 MiB JSON response.
	MaxDeliverableBytes = 512 << 10
	// MaxDeliverables bounds a listing.
	MaxDeliverables = 200
	// maxDeliverableEntries bounds how many directory entries are examined.
	maxDeliverableEntries = 4096
)

var deliverableNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,127}$`)

// Deliverable describes one served file.
type Deliverable struct {
	Name       string    `json:"name"`
	Bytes      int       `json:"bytes"`
	SHA256     string    `json:"sha256"`
	ModifiedAt time.Time `json:"modified_at"`
}

// DeliverableContent is a served file with its bytes.
type DeliverableContent struct {
	Name          string `json:"name"`
	Bytes         int    `json:"bytes"`
	SHA256        string `json:"sha256"`
	ContentBase64 string `json:"content_base64"`
}

// openDeliverablesDir opens a home's deliverables directory. It returns
// (-1, nil) when the home has none.
func (s *Supervisor) openDeliverablesDir(installation, home string) (int, error) {
	if err := validateHomeRef(installation, home); err != nil {
		return -1, err
	}
	// Walk from the supervisor's own homes directory one component at a
	// time, each opened relative to the last and never through a link.
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open(s.homesDir(), flags, 0)
	if err != nil {
		return -1, apiErr(http.StatusNotFound, "home_unknown", "no such home")
	}
	for i, component := range []string{HomeHash(installation, home), homeDataDir, DeliverablesDir} {
		next, err := unix.Openat(fd, component, flags, 0)
		unix.Close(fd)
		if err != nil {
			if i < 2 {
				return -1, apiErr(http.StatusNotFound, "home_unknown", "no such home")
			}
			// Absent, or not a real directory: there are no deliverables.
			return -1, nil
		}
		fd = next
	}
	return fd, nil
}

// readDeliverable reads one file relative to an open deliverables directory.
// ok is false for anything that is not a regular file of acceptable size.
func readDeliverable(dirFD int, name string) (data []byte, modified time.Time, tooLarge, ok bool) {
	if !deliverableNameRe.MatchString(name) {
		return nil, time.Time{}, false, false
	}
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, time.Time{}, false, false
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	// Stat the descriptor, not the name: the name may already point elsewhere.
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, time.Time{}, false, false
	}
	if info.Size() > MaxDeliverableBytes {
		return nil, time.Time{}, true, false
	}
	// The size may change under us: read from this descriptor with a bound
	// and judge what was actually read.
	data, err = io.ReadAll(io.LimitReader(f, MaxDeliverableBytes+1))
	if err != nil {
		return nil, time.Time{}, false, false
	}
	if len(data) > MaxDeliverableBytes {
		return nil, time.Time{}, true, false
	}
	return data, info.ModTime().UTC().Truncate(time.Second), false, true
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Deliverables lists the files a home's worker left for its requester.
func (s *Supervisor) Deliverables(installation, home string) ([]Deliverable, bool, error) {
	dirFD, err := s.openDeliverablesDir(installation, home)
	if err != nil {
		return nil, false, err
	}
	out := []Deliverable{}
	if dirFD < 0 {
		return out, false, nil
	}
	dir := os.NewFile(uintptr(dirFD), DeliverablesDir)
	defer dir.Close()
	names, readErr := dir.Readdirnames(maxDeliverableEntries + 1)
	if readErr != nil && readErr != io.EOF {
		return nil, false, apiErr(http.StatusServiceUnavailable, "deliverables_unavailable", "deliverables could not be listed")
	}
	truncated := len(names) > maxDeliverableEntries
	sort.Strings(names)
	for _, name := range names {
		data, modified, _, ok := readDeliverable(dirFD, name)
		if !ok {
			continue
		}
		if len(out) == MaxDeliverables {
			truncated = true
			break
		}
		out = append(out, Deliverable{Name: name, Bytes: len(data), SHA256: digestOf(data), ModifiedAt: modified})
	}
	return out, truncated, nil
}

// Deliverable returns one file. When wantSHA256 is set, the file is returned
// only if its bytes have that digest.
func (s *Supervisor) Deliverable(installation, home, name, wantSHA256 string) (*DeliverableContent, error) {
	if !deliverableNameRe.MatchString(name) {
		return nil, apiErr(http.StatusNotFound, "deliverable_unknown", "no such deliverable")
	}
	if wantSHA256 != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(wantSHA256) {
		return nil, apiErr(http.StatusBadRequest, "request_invalid", "sha256 must be 64 lowercase hex characters")
	}
	dirFD, err := s.openDeliverablesDir(installation, home)
	if err != nil {
		return nil, err
	}
	if dirFD < 0 {
		return nil, apiErr(http.StatusNotFound, "deliverable_unknown", "no such deliverable")
	}
	defer unix.Close(dirFD)
	data, _, tooLarge, ok := readDeliverable(dirFD, name)
	if tooLarge {
		return nil, apiErr(http.StatusRequestEntityTooLarge, "deliverable_too_large", "deliverable is larger than %d bytes", MaxDeliverableBytes)
	}
	if !ok {
		return nil, apiErr(http.StatusNotFound, "deliverable_unknown", "no such deliverable")
	}
	digest := digestOf(data)
	if wantSHA256 != "" && wantSHA256 != digest {
		return nil, apiErr(http.StatusConflict, "deliverable_changed", "the deliverable no longer has that digest")
	}
	return &DeliverableContent{Name: name, Bytes: len(data), SHA256: digest, ContentBase64: base64.StdEncoding.EncodeToString(data)}, nil
}
