//go:build !linux && !darwin && !windows

package device

func readMachineID() string { return "" }
