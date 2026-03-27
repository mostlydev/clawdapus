package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	updateCheckURL      = "https://api.github.com/repos/mostlydev/clawdapus/releases/latest"
	updateCheckInterval = 24 * time.Hour
	updateCheckFile     = ".claw-update-check"
)

type updateCheckCache struct {
	CheckedAt  time.Time `json:"checked_at"`
	LatestTag  string    `json:"latest_tag"`
	NotifyOnce bool      `json:"notify_once"`
}

// checkForUpdate fetches the latest release tag from GitHub and returns it.
// Returns empty string on any error (best-effort, never blocks the user).
func checkForUpdate() string {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest("GET", updateCheckURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "claw/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	return strings.TrimPrefix(payload.TagName, "v")
}

func cacheFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claw", updateCheckFile)
}

func readCache() *updateCheckCache {
	path := cacheFile()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c updateCheckCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}

func writeCache(c *updateCheckCache) {
	path := cacheFile()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// maybeNotifyUpdate prints an update notice if a newer release is available.
// Checks at most once per day; never blocks on network errors.
func maybeNotifyUpdate() {
	if version == "dev" {
		return
	}

	cache := readCache()
	if cache != nil && time.Since(cache.CheckedAt) < updateCheckInterval {
		if cache.LatestTag != "" && cache.LatestTag != version {
			printUpdateNotice(cache.LatestTag)
		}
		return
	}

	latest := checkForUpdate()
	writeCache(&updateCheckCache{
		CheckedAt: time.Now(),
		LatestTag: latest,
	})

	if latest != "" && latest != version {
		printUpdateNotice(latest)
	}
}

func printUpdateNotice(latest string) {
	fmt.Fprintf(os.Stderr, "\n  Update available: v%s → v%s  (run: claw update)\n\n", version, latest)
}
