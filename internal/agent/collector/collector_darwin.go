//go:build darwin

package collector

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func osVersion() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func cpuModel() string {
	out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ramMegabytes() int64 {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	b, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return b / (1024 * 1024)
}

func diskTotal() string {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return ""
	}
	return humanBytes(st.Blocks * uint64(st.Bsize))
}

func serialNumber() string {
	out, err := exec.Command("ioreg", "-l").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "IOPlatformSerialNumber") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), "\"")
			}
		}
	}
	return ""
}

// installedSoftware читает список приложений через system_profiler (один процесс,
// структурированный JSON). Может занять пару секунд — инвентаризация редкая.
func installedSoftware() []Software {
	out, err := exec.Command("system_profiler", "-json", "SPApplicationsDataType").Output()
	if err != nil {
		return nil
	}
	var data struct {
		Apps []struct {
			Name    string `json:"_name"`
			Version string `json:"version"`
		} `json:"SPApplicationsDataType"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil
	}
	sw := make([]Software, 0, len(data.Apps))
	for _, a := range data.Apps {
		if a.Name == "" {
			continue
		}
		sw = append(sw, Software{Name: a.Name, Version: a.Version})
	}
	return sw
}
