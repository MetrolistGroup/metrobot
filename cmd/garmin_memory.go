package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	GarminMemoryFile    = "garmin-memory.md"
	garminMemoryMaxSize = 16 * 1024
	garminMemoryEmpty   = "# Garmin Memory\n"
)

type GarminMemory struct {
	path string
	mu   sync.RWMutex
}

func NewGarminMemory(path string) (*GarminMemory, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("memory file path is required")
	}

	memory := &GarminMemory{path: path}
	if _, err := os.Stat(path); err == nil {
		return memory, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking Garmin memory: %w", err)
	}
	if err := memory.write(garminMemoryEmpty); err != nil {
		return nil, err
	}
	return memory, nil
}

func (m *GarminMemory) Read() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		return "", fmt.Errorf("reading Garmin memory: %w", err)
	}
	if len(data) > garminMemoryMaxSize {
		return "", fmt.Errorf("Garmin memory exceeds %d bytes", garminMemoryMaxSize)
	}
	return strings.TrimSpace(string(data)), nil
}

func (m *GarminMemory) Append(content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("memory content is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("reading Garmin memory: %w", err)
	}
	current := strings.TrimSpace(string(data))
	updated := current + "\n\n" + content + "\n"
	if current == "" {
		updated = garminMemoryEmpty + "\n" + content + "\n"
	}
	return m.writeLocked(updated)
}

func (m *GarminMemory) Replace(content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("memory content is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeLocked(content + "\n")
}

func (m *GarminMemory) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeLocked(garminMemoryEmpty)
}

func (m *GarminMemory) write(content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeLocked(content)
}

func (m *GarminMemory) writeLocked(content string) error {
	if len(content) > garminMemoryMaxSize {
		return fmt.Errorf("Garmin memory cannot exceed %d bytes", garminMemoryMaxSize)
	}

	dir := filepath.Dir(m.path)
	tmp, err := os.CreateTemp(dir, ".garmin-memory-*")
	if err != nil {
		return fmt.Errorf("creating Garmin memory file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing Garmin memory file: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing Garmin memory: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing Garmin memory: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing Garmin memory: %w", err)
	}
	if err := os.Rename(tmpPath, m.path); err != nil {
		return fmt.Errorf("saving Garmin memory: %w", err)
	}
	return nil
}
