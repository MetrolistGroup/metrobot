package discord

import (
	"path/filepath"
	"testing"

	"github.com/MetrolistGroup/metrobot/cmd"
)

func TestGarminMemoryAccessorSharesInstance(t *testing.T) {
	bot := &Bot{}
	if bot.GarminMemory() != nil {
		t.Fatal("disabled AI unexpectedly has shared memory")
	}
	memory, err := cmd.NewGarminMemory(filepath.Join(t.TempDir(), "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	bot.garminMemory = memory
	if bot.GarminMemory() != memory {
		t.Fatal("dashboard accessor returned another memory instance")
	}
}
