package shared

import (
	"bytes"
	"context"
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

func ExecInContainer(
	ctx context.Context,
	cli *client.Client,
	containerID string,
	cmd []string,
) (stdout, stderr string, exitCode int, err error) {
	execCfg := types.ExecConfig{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execID, err := cli.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return "", "", -1, fmt.Errorf("exec create failed: %w", err)
	}

	resp, err := cli.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{})
	if err != nil {
		return "", "", -1, fmt.Errorf("exec attach failed: %w", err)
	}
	defer resp.Close()

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(&stdoutBuf, &stderrBuf, resp.Reader)
		copyDone <- copyErr
	}()

	select {
	case copyErr := <-copyDone:
		if copyErr != nil {
			return "", "", -1, fmt.Errorf("exec read failed: %w", copyErr)
		}
	case <-ctx.Done():
		resp.Close()
		return "", "", -1, fmt.Errorf("exec timed out")
	}

	execInspect, err := cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return "", "", -1, fmt.Errorf("exec inspect failed: %w", err)
	}
	return stdoutBuf.String(), stderrBuf.String(), execInspect.ExitCode, nil
}
