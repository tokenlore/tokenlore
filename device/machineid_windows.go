package device

import "golang.org/x/sys/windows/registry"

func readMachineID() string {
	// WOW64_64KEY: Auch ein 32-Bit-Programm liest den Wert der 64-Bit-Ansicht.
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return v
}
