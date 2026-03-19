package recycle

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// IsAlive checks whether a process with the given PID is still running.
// Uses kill -0 (signal 0) which checks existence without sending a signal.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// GracefulKill sends SIGTERM and waits up to gracePeriod for the process to die.
// If the process is still alive after the grace period, sends SIGKILL.
// Returns nil if the process is confirmed dead. Returns an error only if
// SIGKILL also fails to terminate the process.
func GracefulKill(pid int, gracePeriod time.Duration) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	if !IsAlive(pid) {
		return nil // already dead
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil // can't find it, treat as dead
	}

	// Send SIGTERM
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if !IsAlive(pid) {
			return nil
		}
		return fmt.Errorf("send SIGTERM to %d: %w", pid, err)
	}

	// Poll for death at 1s intervals
	deadline := time.Now().Add(gracePeriod)
	for time.Now().Before(deadline) {
		if !IsAlive(pid) {
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	// Force kill
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		if !IsAlive(pid) {
			return nil
		}
		return fmt.Errorf("send SIGKILL to %d: %w", pid, err)
	}

	// Final check
	time.Sleep(500 * time.Millisecond)
	if IsAlive(pid) {
		return fmt.Errorf("pid %d still alive after SIGKILL", pid)
	}
	return nil
}

// WasForceKilled returns true if SIGKILL was required (for degraded logging).
// Call this after GracefulKill — it checks if the process died within the
// SIGTERM grace window by comparing elapsed time.
// This is a heuristic: if GracefulKill took longer than gracePeriod, force-kill was used.
func WasForceKilled(startTime time.Time, gracePeriod time.Duration) bool {
	return time.Since(startTime) >= gracePeriod
}
