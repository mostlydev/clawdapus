package persona

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

type ResolvedPersona struct {
	Ref      string
	HostPath string
}

func Materialize(baseDir, runtimeDir, ref string) (*ResolvedPersona, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}

	targetDir := filepath.Join(runtimeDir, "persona")
	if err := os.RemoveAll(targetDir); err != nil {
		return nil, fmt.Errorf("reset persona dir: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return nil, fmt.Errorf("create persona dir: %w", err)
	}

	if isLocalRef(ref) {
		src, err := resolveLocalPersonaPath(baseDir, ref)
		if err != nil {
			return nil, err
		}
		if err := copyDir(src, targetDir); err != nil {
			return nil, err
		}
		return &ResolvedPersona{Ref: ref, HostPath: targetDir}, nil
	}

	if err := pullRemotePersona(context.Background(), targetDir, ref); err != nil {
		return nil, err
	}
	return &ResolvedPersona{Ref: ref, HostPath: targetDir}, nil
}

func isLocalRef(ref string) bool {
	return strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "file://")
}

func resolveLocalPersonaPath(baseDir, ref string) (string, error) {
	path := strings.TrimPrefix(ref, "file://")
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base dir %q: %w", baseDir, err)
	}
	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		return "", fmt.Errorf("resolve real base dir %q: %w", baseDir, err)
	}

	hostPath, err := filepath.Abs(filepath.Join(baseDir, path))
	if err != nil {
		return "", fmt.Errorf("resolve persona path %q: %w", ref, err)
	}
	if !strings.HasPrefix(hostPath, absBase+string(filepath.Separator)) && hostPath != absBase {
		return "", fmt.Errorf("persona path %q escapes base directory %q", ref, baseDir)
	}

	info, err := os.Stat(hostPath)
	if err != nil {
		return "", fmt.Errorf("persona path %q not found: %w", hostPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("persona path %q is not a directory", ref)
	}

	realHostPath, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		return "", fmt.Errorf("resolve real persona path %q: %w", ref, err)
	}
	if !strings.HasPrefix(realHostPath, realBase+string(filepath.Separator)) && realHostPath != realBase {
		return "", fmt.Errorf("persona path %q escapes base directory %q", ref, baseDir)
	}
	return realHostPath, nil
}

func copyDir(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read persona dir %q: %w", srcDir, err)
	}
	for _, entry := range entries {
		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat persona entry %q: %w", srcPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("persona path %q contains unsupported symlink %q", srcDir, srcPath)
		}
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("create persona directory %q: %w", dstPath, err)
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("persona path %q contains unsupported file type at %q", srcDir, srcPath)
		}
		if err := copyFile(srcPath, dstPath, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open persona file %q: %w", srcPath, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create persona file %q: %w", dstPath, err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("copy persona file %q: %w", srcPath, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close persona file %q: %w", dstPath, err)
	}
	return nil
}

func pullRemotePersona(ctx context.Context, targetDir, ref string) error {
	parsed, err := registry.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("parse persona reference %q: %w", ref, err)
	}

	repo, err := remote.NewRepository(parsed.Registry + "/" + parsed.Repository)
	if err != nil {
		return fmt.Errorf("create persona repository %q: %w", ref, err)
	}

	if store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{}); err == nil {
		repo.Client = &auth.Client{
			Credential: credentials.Credential(store),
			Cache:      auth.NewCache(),
		}
	}

	fs, err := file.New(targetDir)
	if err != nil {
		return fmt.Errorf("create persona file store: %w", err)
	}
	defer fs.Close()
	fs.IgnoreNoName = true
	fs.PreservePermissions = true

	if _, err := oras.Copy(ctx, repo, parsed.ReferenceOrDefault(), fs, "latest", oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("pull persona %q: %w", ref, err)
	}
	return nil
}
