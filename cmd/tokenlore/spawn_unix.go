//go:build !windows

package main

import "syscall"

// spawnBackground startet exe in einer eigenen Sitzung (Setsid), damit es weder am Terminal noch an der
// Prozessgruppe des Hooks hängt, und gibt den Prozess sofort frei.
func spawnBackground(dataDir, exe string, args ...string) error {
	cmd := backgroundCmd(dataDir, exe, args)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
