package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGarminMemoryLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.md")
	memory, err := NewGarminMemory(path)
	if err != nil {
		t.Fatalf("NewGarminMemory() error = %v", err)
	}

	if err := memory.Append("- Remember this"); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	content, err := memory.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !strings.Contains(content, "# Metrobot Memory") || !strings.Contains(content, "Remember this") {
		t.Fatalf("Read() = %q", content)
	}

	if err := memory.Replace("# Custom\n\nNew content"); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	content, _ = memory.Read()
	if content != "# Custom\n\nNew content" {
		t.Fatalf("Read() after replace = %q", content)
	}

	if err := memory.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	content, _ = memory.Read()
	if content != "# Metrobot Memory" {
		t.Fatalf("Read() after clear = %q", content)
	}
}

func TestGarminMemoryMigratesLegacyHeading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.md")
	if err := os.WriteFile(path, []byte("# Garmin Memory\n\n- Existing fact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	memory, err := NewGarminMemory(path)
	if err != nil {
		t.Fatalf("NewGarminMemory() error = %v", err)
	}
	content, err := memory.Read()
	if err != nil {
		t.Fatal(err)
	}
	if content != "# Metrobot Memory\n\n- Existing fact" {
		t.Fatalf("migrated memory = %q", content)
	}
}

func TestGarminMemoryRejectsOversizedContent(t *testing.T) {
	memory, err := NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Replace(strings.Repeat("x", garminMemoryMaxSize+1)); err == nil {
		t.Fatal("Replace() error = nil, want size error")
	}
}
