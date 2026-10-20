package runs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const (
	composeUpTimeout   = 2 * time.Minute
	composeDownTimeout = time.Minute
	dockerReadTimeout  = 20 * time.Second
)

// DockerExecutor runs `docker compose` for lifecycle (ADR-002) and uses the
// Docker SDK for reads only.
type DockerExecutor struct {
	cli  *client.Client
	Logf func(format string, args ...any)
}

func NewDockerExecutor() (*DockerExecutor, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &DockerExecutor{cli: cli, Logf: func(string, ...any) {}}, nil
}

func (d *DockerExecutor) Close() error { return d.cli.Close() }

// Up creates and starts a run's project. Inputs reach Compose only through
// the process environment; compose output is discarded because it can echo
// interpolated configuration.
func (d *DockerExecutor) Up(ctx context.Context, project, file string, env map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, composeUpTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", project, "--project-directory", filepath.Dir(file), "-f", file, "up", "-d", "--no-build", "--pull", "never")
	cmd.Env = composeEnv(env)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		d.Logf("[claw runs] %s: compose up exited: %v", project, exitSummary(err))
		return errors.New("compose up failed")
	}
	return nil
}

// Down removes a run's project and orphans. It never relies on ambient
// Compose discovery: with the recorded file it uses that file; if the file is
// gone it passes a minimal empty project on stdin, which removes only the
// deterministic project's containers. The project directory is pinned so no
// stray .env file is read.
func (d *DockerExecutor) Down(ctx context.Context, project, file string) error {
	ctx, cancel := context.WithTimeout(ctx, composeDownTimeout)
	defer cancel()
	dir := filepath.Dir(file)
	args := []string{"compose", "-p", project}
	var stdin io.Reader
	if info, err := os.Lstat(file); err == nil && info.Mode().IsRegular() {
		args = append(args, "--project-directory", dir, "-f", file)
	} else {
		empty, err := os.MkdirTemp("", "clawrun-down-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(empty)
		args = append(args, "--project-directory", empty, "-f", "-")
		stdin = strings.NewReader("name: " + project + "\nservices: {}\n")
	}
	args = append(args, "down", "--remove-orphans", "--timeout", "10")
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = composeEnv(nil)
	cmd.Stdin = stdin
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		d.Logf("[claw runs] %s: compose down exited: %v", project, exitSummary(err))
		return errors.New("compose down failed")
	}
	return nil
}

// Containers lists every container, running or not, labelled with the key.
func (d *DockerExecutor) Containers(ctx context.Context, keyHash string) ([]Container, error) {
	ctx, cancel := context.WithTimeout(ctx, dockerReadTimeout)
	defer cancel()
	list, err := d.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", LabelRunKey+"="+keyHash)),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(list))
	for _, c := range list {
		info, err := d.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		item := Container{ID: c.ID, ImageID: info.Image, Labels: c.Labels}
		if info.State != nil {
			item.Running = info.State.Running || info.State.Restarting
			switch info.State.Status {
			case "exited", "dead":
				item.Exited = true
				item.ExitCode = info.State.ExitCode
			case "created":
				// Created but never started: treat as not running; the
				// supervisor's spawn path decides.
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// Stdout returns at most limit bytes of the container's stdout, and whether
// the stream held more.
func (d *DockerExecutor) Stdout(ctx context.Context, containerID string, limit int) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, dockerReadTimeout)
	defer cancel()
	rc, err := d.cli.ContainerLogs(ctx, containerID, container.LogsOptions{ShowStdout: true})
	if err != nil {
		return nil, false, err
	}
	defer rc.Close()
	// One sentinel byte beyond the limit tells a complete stream from a cut one.
	stdout := &limitedBuffer{limit: limit + 1}
	if _, err := stdcopy.StdCopy(stdout, io.Discard, rc); err != nil && !errors.Is(err, errLimitReached) {
		return nil, false, err
	}
	raw := stdout.Bytes()
	if len(raw) > limit {
		return raw[:limit], true, nil
	}
	return raw, false, nil
}

var errLimitReached = errors.New("limit reached")

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.Len()
	if room <= 0 {
		return 0, errLimitReached
	}
	if len(p) > room {
		b.Buffer.Write(p[:room])
		return room, errLimitReached
	}
	return b.Buffer.Write(p)
}

// composeEnv passes the supervisor's environment minus any inherited
// CLAW_RUN_* values, plus this run's inputs.
func composeEnv(inputs map[string]string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(inputs))
	for _, kv := range base {
		if strings.HasPrefix(kv, "CLAW_RUN_") {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range inputs {
		out = append(out, k+"="+v)
	}
	return out
}

func exitSummary(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("exit status %d", exit.ExitCode())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	return "failed to run"
}

// RunWorkers lists every run-worker container labelled for pod.
func (d *DockerExecutor) RunWorkers(ctx context.Context, pod string) ([]Container, error) {
	ctx, cancel := context.WithTimeout(ctx, dockerReadTimeout)
	defer cancel()
	list, err := d.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", LabelRole+"="+RoleRunWorker), filters.Arg("label", LabelPod+"="+pod)),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(list))
	for _, c := range list {
		out = append(out, Container{ID: c.ID, ImageID: c.ImageID, Labels: c.Labels, Running: c.State == "running"})
	}
	return out, nil
}
