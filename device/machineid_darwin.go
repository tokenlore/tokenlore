package device

import (
	"context"
	"os/exec"
	"time"
)

func readMachineID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	return parseIOPlatformUUID(string(out))
}
