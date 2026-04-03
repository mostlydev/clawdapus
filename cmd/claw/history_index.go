package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	historyIndexVersion         = 1
	historyIndexCheckpointEvery = 128
)

type historyIndexFile struct {
	Version     int                      `json:"version"`
	HistorySize int64                    `json:"history_size"`
	EntryCount  int64                    `json:"entry_count"`
	Checkpoints []historyIndexCheckpoint `json:"checkpoints,omitempty"`
}

type historyIndexCheckpoint struct {
	Offset int64  `json:"offset"`
	TS     string `json:"ts"`
}

func historyReadStartOffset(historyPath string, after *time.Time) (int64, error) {
	if after == nil {
		return 0, nil
	}
	index, err := ensureHistoryIndex(historyPath)
	if err != nil {
		return 0, err
	}
	if index == nil || len(index.Checkpoints) == 0 {
		return 0, nil
	}

	best := int64(0)
	for _, checkpoint := range index.Checkpoints {
		ts, err := time.Parse(time.RFC3339, checkpoint.TS)
		if err != nil {
			return 0, fmt.Errorf("parse history index checkpoint timestamp %q: %w", checkpoint.TS, err)
		}
		if ts.After(*after) {
			break
		}
		best = checkpoint.Offset
	}
	return best, nil
}

func ensureHistoryIndex(historyPath string) (*historyIndexFile, error) {
	info, err := os.Stat(historyPath)
	if err != nil {
		return nil, err
	}

	size := info.Size()
	indexPath := historyIndexPath(historyPath)
	index, err := loadHistoryIndex(indexPath)
	if err != nil {
		return nil, err
	}

	startOffset := int64(0)
	if index == nil || index.Version != historyIndexVersion || index.HistorySize > size {
		index = &historyIndexFile{Version: historyIndexVersion}
	} else {
		startOffset = index.HistorySize
	}

	if startOffset == size {
		return index, nil
	}

	f, err := os.Open(historyPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(f)
	offset := startOffset
	entryCount := index.EntryCount
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				var header historyEntryEnvelope
				if err := json.Unmarshal(trimmed, &header); err != nil {
					return nil, fmt.Errorf("parse history entry for index: %w", err)
				}
				if entryCount%historyIndexCheckpointEvery == 0 {
					index.Checkpoints = append(index.Checkpoints, historyIndexCheckpoint{
						Offset: offset,
						TS:     header.TS,
					})
				}
				entryCount++
			}
			offset += int64(len(line))
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return nil, err
	}

	index.EntryCount = entryCount
	index.HistorySize = offset
	if err := writeHistoryIndex(indexPath, index); err != nil {
		return nil, err
	}
	return index, nil
}

func historyIndexPath(historyPath string) string {
	return filepath.Join(filepath.Dir(historyPath), "history.index.json")
}

func loadHistoryIndex(path string) (*historyIndexFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var index historyIndexFile
	if err := json.Unmarshal(raw, &index); err != nil {
		return nil, nil
	}
	return &index, nil
}

func writeHistoryIndex(path string, index *historyIndexFile) error {
	raw, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
