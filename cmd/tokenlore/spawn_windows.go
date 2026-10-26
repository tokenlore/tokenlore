package main

import (
	"errors"
	"syscall"
)

const (
	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// spawnBackground startet exe ohne Konsole und außerhalb der Prozessgruppe des Hooks. Ausbrechen aus dem
// Job ist nötig, weil Claude Code den Hook in einem Job beenden kann; erlaubt der Job das nicht
// (Zugriff verweigert), wird einmal ohne Ausbrechen gestartet.
func spawnBackground(dataDir, exe string, args ...string) error {
	err := start(dataDir, exe, args, detachedProcess|createNewProcessGroup|createBreakawayFromJob)
	if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		err = start(dataDir, exe, args, detachedProcess|createNewProcessGroup)
	}
	return err
}

func start(dataDir, exe string, args []string, flags uint32) error {
	cmd := backgroundCmd(dataDir, exe, args)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
