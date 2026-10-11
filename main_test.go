package main

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"go.uber.org/zap"
)

func TestWaitForShutdownExitCode(t *testing.T) {
	for _, failure := range []bool{false, true} {
		signals := make(chan os.Signal, 1)
		serverErrors := make(chan error, 1)
		want := 0
		if failure {
			serverErrors <- errors.New("dashboard listener failed")
			want = 1
		} else {
			signals <- syscall.SIGTERM
		}
		if got := waitForShutdown(signals, serverErrors, zap.NewNop()); got != want {
			t.Fatalf("failure=%v: exit code=%d, want %d", failure, got, want)
		}
	}
}
