package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	GarminMemoryFile           = "garmin-memory.md"
	garminMemoryMaxSize        = 16 * 1024
	garminLearnedMemoryMaxSize = 6 * 1024
	garminMemoryEmpty          = "# Metrobot Memory\n"
	garminLearnedMemoryStart   = "<!-- learned-conversation-memory:start -->"
	garminLearnedMemoryEnd     = "<!-- learned-conversation-memory:end -->"
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
		if err := memory.migrateHeading(); err != nil {
			return nil, err
		}
		return memory, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking Metrobot memory: %w", err)
	}
	if err := memory.write(garminMemoryEmpty); err != nil {
		return nil, err
	}
	return memory, nil
}

func (m *GarminMemory) migrateHeading() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("reading Metrobot memory: %w", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "# Garmin Memory") {
		return nil
	}
	return m.write(strings.Replace(content, "# Garmin Memory", "# Metrobot Memory", 1))
}

func (m *GarminMemory) Read() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		return "", fmt.Errorf("reading Metrobot memory: %w", err)
	}
	if len(data) > garminMemoryMaxSize {
		return "", fmt.Errorf("Metrobot memory exceeds %d bytes", garminMemoryMaxSize)
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
		return fmt.Errorf("reading Metrobot memory: %w", err)
	}
	current := strings.TrimSpace(string(data))
	updated := current + "\n\n" + content + "\n"
	if current == "" {
		updated = garminMemoryEmpty + "\n" + content + "\n"
	}
	return m.writeLocked(updated)
}

func (m *GarminMemory) AppendLearned(facts []string) error {
	incoming := make([]string, 0, len(facts))
	for _, fact := range facts {
		fact = strings.Join(strings.Fields(strings.TrimLeft(fact, "-* \t")), " ")
		lower := strings.ToLower(fact)
		blocked := fact == "" || lower == "none" || len([]rune(fact)) > 300
		for _, phrase := range []string{"discord_", "<@", "password", "api key", "secret", "token", "ignore previous", "system prompt", "you must", "you should", "follow these instructions"} {
			blocked = blocked || strings.Contains(lower, phrase)
		}
		if blocked {
			continue
		}
		incoming = append(incoming, fact)
		if len(incoming) == 5 {
			break
		}
	}
	if len(incoming) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("reading Metrobot memory: %w", err)
	}
	current := strings.TrimSpace(string(data))
	prefix, suffix := current, ""
	var learned []string
	if start := strings.Index(current, garminLearnedMemoryStart); start >= 0 {
		if end := strings.Index(current[start:], garminLearnedMemoryEnd); end >= 0 {
			end += start
			prefix = strings.TrimSpace(current[:start])
			suffix = strings.TrimSpace(current[end+len(garminLearnedMemoryEnd):])
			for _, line := range strings.Split(current[start+len(garminLearnedMemoryStart):end], "\n") {
				if fact := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- ")); fact != "" && !strings.HasPrefix(fact, "#") {
					learned = append(learned, fact)
				}
			}
		}
	}
	existing := make(map[string]struct{}, len(learned))
	for _, fact := range learned {
		existing[strings.ToLower(fact)] = struct{}{}
	}
	for _, fact := range incoming {
		if _, ok := existing[strings.ToLower(fact)]; !ok {
			learned = append(learned, fact)
			existing[strings.ToLower(fact)] = struct{}{}
		}
	}
	build := func() string {
		parts := []string{prefix}
		if len(learned) > 0 {
			parts = append(parts, garminLearnedMemoryStart+"\n## Learned from conversations\n\n- "+strings.Join(learned, "\n- ")+"\n"+garminLearnedMemoryEnd)
		}
		if suffix != "" {
			parts = append(parts, suffix)
		}
		return strings.Join(parts, "\n\n") + "\n"
	}
	updated := build()
	for len(learned) > 0 && (len(updated) > garminMemoryMaxSize || len(strings.Join(learned, "\n")) > garminLearnedMemoryMaxSize) {
		learned = learned[1:]
		updated = build()
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
		return fmt.Errorf("Metrobot memory cannot exceed %d bytes", garminMemoryMaxSize)
	}

	dir := filepath.Dir(m.path)
	tmp, err := os.CreateTemp(dir, ".garmin-memory-*")
	if err != nil {
		return fmt.Errorf("creating Metrobot memory file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing Metrobot memory file: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing Metrobot memory: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing Metrobot memory: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing Metrobot memory: %w", err)
	}
	if err := os.Rename(tmpPath, m.path); err != nil {
		return fmt.Errorf("saving Metrobot memory: %w", err)
	}
	return nil
}
