package recycle

import (
	"os"
	"testing"
	"time"
)

func TestIsAlive_Self(t *testing.T) {
	// Our own PID should be alive
	if !IsAlive(os.Getpid()) {
		t.Error("expected self to be alive")
	}
}

func TestIsAlive_InvalidPID(t *testing.T) {
	if IsAlive(0) {
		t.Error("expected pid 0 to be not alive")
	}
	if IsAlive(-1) {
		t.Error("expected pid -1 to be not alive")
	}
}

func TestIsAlive_NonexistentPID(t *testing.T) {
	// PID 4194304+ is unlikely to exist on most systems
	if IsAlive(4194304) {
		t.Skip("PID 4194304 unexpectedly exists")
	}
}

func TestGracefulKill_AlreadyDead(t *testing.T) {
	// Should return nil for a non-existent PID
	err := GracefulKill(4194304, 1*time.Second)
	if err != nil {
		t.Errorf("expected nil for dead PID, got: %v", err)
	}
}

func TestGracefulKill_InvalidPID(t *testing.T) {
	err := GracefulKill(0, 1*time.Second)
	if err == nil {
		t.Error("expected error for PID 0")
	}
}

func TestWasForceKilled(t *testing.T) {
	grace := 5 * time.Second

	// Started recently, not force-killed
	if WasForceKilled(time.Now(), grace) {
		t.Error("should not be force-killed when started just now")
	}

	// Started long ago, was force-killed
	if !WasForceKilled(time.Now().Add(-10*time.Second), grace) {
		t.Error("should be force-killed when elapsed > grace")
	}
}
