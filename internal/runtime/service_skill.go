package runtime

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// GenerateServiceSkillFallback produces a markdown skill file describing
// how to reach a service surface when no service-emitted skill exists.
func GenerateServiceSkillFallback(target string, ports []string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# %s (service surface)\n\n", target))
	b.WriteString("## Connection\n")
	b.WriteString(fmt.Sprintf("- **Hostname:** %s\n", target))
	b.WriteString("- **Network:** claw-internal (pod-internal, no external access)\n")
	if len(ports) > 0 {
		b.WriteString(fmt.Sprintf("- **Ports:** %s\n", strings.Join(ports, ", ")))
	}
	b.WriteString("\n## Usage\n")
	b.WriteString(fmt.Sprintf("This service is available to you within the pod network.\n"))
	b.WriteString(fmt.Sprintf("Use the hostname `%s` to connect.\n", target))
	b.WriteString("Credentials, if required, are provided via environment variables.\n")

	return b.String()
}

// ExtractServiceSkill extracts a skill file from a service image using Docker.
// Creates a temporary container (without starting it), copies the file out,
// then removes the container.
func ExtractServiceSkill(imageRef string, skillEmitPath string) ([]byte, error) {
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer docker.Close()

	ctx := context.Background()

	// Create a temporary container (don't start it)
	resp, err := docker.ContainerCreate(ctx, &container.Config{Image: imageRef}, nil, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("create temp container from %q: %w", imageRef, err)
	}
	defer docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{})

	// Copy the file from the container
	reader, _, err := docker.CopyFromContainer(ctx, resp.ID, skillEmitPath)
	if err != nil {
		return nil, fmt.Errorf("copy %q from %q: %w", skillEmitPath, imageRef, err)
	}
	defer reader.Close()

	// Read from tar stream
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("file %q not found in tar stream from %q", skillEmitPath, imageRef)
		}
		if err != nil {
			return nil, fmt.Errorf("read tar from %q: %w", imageRef, err)
		}
		if header.Typeflag == tar.TypeReg {
			content, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read file %q from %q: %w", skillEmitPath, imageRef, err)
			}
			return content, nil
		}
	}
}
