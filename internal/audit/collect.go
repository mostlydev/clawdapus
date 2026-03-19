package audit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/docker/docker/api/types"
	containerapi "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

func CollectPodEvents(ctx context.Context, cli *client.Client, podName string, since time.Time) ([]Event, int, error) {
	if cli == nil {
		return nil, 0, fmt.Errorf("docker client is nil")
	}
	if podName == "" {
		return nil, 0, fmt.Errorf("pod name is required")
	}

	args := filters.NewArgs(
		filters.Arg("label", "claw.pod="+podName),
		filters.Arg("label", "claw.role=proxy"),
	)
	containers, err := cli.ContainerList(ctx, containerapi.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, 0, fmt.Errorf("list pod proxy containers: %w", err)
	}
	slices.SortFunc(containers, func(a, b types.Container) int {
		return compareStrings(a.Labels["claw.service"], b.Labels["claw.service"])
	})

	events := make([]Event, 0)
	skipped := 0
	for _, ctr := range containers {
		serviceName := ctr.Labels["claw.service"]
		logs, err := cli.ContainerLogs(ctx, ctr.ID, containerapi.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Since:      formatSince(since),
			Tail:       "all",
		})
		if err != nil {
			return nil, skipped, fmt.Errorf("container logs for %q: %w", serviceName, err)
		}

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		_, copyErr := stdcopy.StdCopy(&stdout, &stderr, logs)
		_ = logs.Close()
		if copyErr != nil {
			return nil, skipped, fmt.Errorf("decode docker log stream for %q: %w", serviceName, copyErr)
		}

		combined, sourceSkipped, err := parseServiceLogs(serviceName, &stdout, &stderr)
		if err != nil {
			return nil, skipped + sourceSkipped, err
		}
		skipped += sourceSkipped
		events = append(events, combined...)
	}
	return events, skipped, nil
}

func parseServiceLogs(serviceName string, readers ...io.Reader) ([]Event, int, error) {
	events := make([]Event, 0)
	skipped := 0
	for _, reader := range readers {
		if reader == nil {
			continue
		}
		parsed, sourceSkipped, err := ParseReader(reader)
		if err != nil {
			return nil, skipped + sourceSkipped, err
		}
		for i := range parsed {
			if parsed[i].SourceService == "" {
				parsed[i].SourceService = serviceName
			}
		}
		skipped += sourceSkipped
		events = append(events, parsed...)
	}
	return events, skipped, nil
}

func formatSince(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	return since.UTC().Format(time.RFC3339Nano)
}

func compareStrings(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
