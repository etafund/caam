//go:build windows

package signals

import (
	"fmt"
	"os"
)

func SendHUP(pid int) error {
	return fmt.Errorf("SIGHUP not supported on Windows (pid=%d)", pid)
}

func SendTerm(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := p.Kill(); err != nil {
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}
	return nil
}
