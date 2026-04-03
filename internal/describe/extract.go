package describe

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

func LoadFromFile(path string) (*ServiceDescriptor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read descriptor %q: %w", path, err)
	}
	return Parse(data)
}

func LoadFromImage(imageRef, descriptorPath string) (*ServiceDescriptor, error) {
	data, err := extractFileFromImage(imageRef, descriptorPath)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func ResolveBuildContextDir(baseDir string, raw interface{}) (string, error) {
	if raw == nil {
		return "", nil
	}

	contextDir := "."
	switch v := raw.(type) {
	case string:
		contextDir = strings.TrimSpace(v)
	case map[string]interface{}:
		if rawContext, ok := v["context"]; ok {
			contextDir = strings.TrimSpace(fmt.Sprint(rawContext))
		}
	default:
		return "", fmt.Errorf("unsupported build value type %T", raw)
	}

	if contextDir == "" {
		contextDir = "."
	}
	if !filepath.IsAbs(contextDir) {
		contextDir = filepath.Join(baseDir, contextDir)
	}
	resolved, err := filepath.Abs(contextDir)
	if err != nil {
		return "", fmt.Errorf("resolve build context %q: %w", contextDir, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat build context %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("build context %q is not a directory", resolved)
	}
	return resolved, nil
}

func LoadFromBuildContext(baseDir string, buildRaw interface{}, descriptorPath string) (*ServiceDescriptor, string, error) {
	contextDir, err := ResolveBuildContextDir(baseDir, buildRaw)
	if err != nil || contextDir == "" {
		return nil, "", err
	}

	for _, candidate := range buildContextCandidates(contextDir, descriptorPath) {
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, "", fmt.Errorf("stat descriptor %q: %w", candidate, err)
		}
		descriptor, err := LoadFromFile(candidate)
		if err != nil {
			return nil, "", err
		}
		return descriptor, candidate, nil
	}

	return nil, "", nil
}

func ResolveBuildContextFile(baseDir string, buildRaw interface{}, containerPath string) (string, error) {
	contextDir, err := ResolveBuildContextDir(baseDir, buildRaw)
	if err != nil || contextDir == "" {
		return "", err
	}

	for _, candidate := range buildContextCandidates(contextDir, containerPath) {
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("stat build-context file %q: %w", candidate, err)
		}
		return candidate, nil
	}

	return "", nil
}

func buildContextCandidates(contextDir, requestedPath string) []string {
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" {
		requestedPath = DefaultDescriptorFile
	}

	trimmed := strings.TrimPrefix(requestedPath, "/")
	candidates := []string{filepath.Join(contextDir, filepath.FromSlash(trimmed))}

	if trimmed == "" || trimmed == "." {
		candidates = []string{filepath.Join(contextDir, DefaultDescriptorFile)}
	}

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func extractFileFromImage(imageRef, path string) ([]byte, error) {
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer docker.Close()

	resp, err := docker.ContainerCreate(context.Background(), &container.Config{Image: imageRef}, nil, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("create temp container from %q: %w", imageRef, err)
	}
	defer docker.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{})

	reader, _, err := docker.CopyFromContainer(context.Background(), resp.ID, path)
	if err != nil {
		if isMissingImagePathError(err) {
			return nil, fmt.Errorf("%w: copy %q from %q", os.ErrNotExist, path, imageRef)
		}
		return nil, fmt.Errorf("copy %q from %q: %w", path, imageRef, err)
	}
	defer reader.Close()

	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%w: file %q not found in tar stream from %q", os.ErrNotExist, path, imageRef)
		}
		if err != nil {
			return nil, fmt.Errorf("read tar from %q: %w", imageRef, err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read file %q from %q: %w", path, imageRef, err)
		}
		return content, nil
	}
}

func isMissingImagePathError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such file or directory") ||
		strings.Contains(text, "could not find the file")
}
