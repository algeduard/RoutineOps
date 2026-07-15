//go:build !darwin && !windows && !linux

package collector

import (
	"bufio"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Прочие unix (freebsd и др.) — не целевые платформы, реализовано для удобства
// разработки/VM. Linux живёт в collector_linux.go и поддержан полноценно.

func osVersion() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	if m := regexp.MustCompile(`(?m)^PRETTY_NAME="?([^"\n]+)"?`).FindStringSubmatch(string(data)); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "model name") {
			if _, v, ok := strings.Cut(line, ":"); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func ramMegabytes() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "MemTotal:") {
			fields := strings.Fields(sc.Text()) // MemTotal:  16384256 kB
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return kb / 1024
				}
			}
		}
	}
	return 0
}

func diskTotal() string {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return ""
	}
	return humanBytes(st.Blocks * uint64(st.Bsize))
}

func serialNumber() string {
	out, err := exec.Command("dmidecode", "-s", "system-serial-number").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// installedSoftware пробует dpkg (Debian/Ubuntu). Best-effort.
func installedSoftware() []Software {
	out, err := exec.Command("dpkg-query", "-W", "-f=${Package}\t${Version}\n").Output()
	if err != nil {
		return nil
	}
	var sw []Software
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, ver, ok := strings.Cut(line, "\t")
		if !ok || name == "" {
			continue
		}
		sw = append(sw, Software{Name: name, Version: ver})
	}
	return sw
}
