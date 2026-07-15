// Package collector собирает сведения об устройстве.
//
// Этап 1: LocalIP() для heartbeat.
// Этап 2: Collect() + InstalledSoftware() для ReportInventory (железо/ОС/ПО).
// Платформенные детали — в файлах collector_<goos>.go (build-теги).
package collector

import (
	"fmt"
	"net"
	"os"
	"runtime"
)

// DeviceInfo — снимок устройства для инвентаризации (маппится в proto.DeviceInfo).
// Публичный IP в этот снимок не входит — сервер вычисляет его сам из адреса
// gRPC-пира (см. clientIP в internal/server/gateway), агент его не отдаёт.
type DeviceInfo struct {
	Hostname  string
	OS        string // macOS / Windows (см. normalizeOS)
	OSVersion string
	CPU       string
	// RAMMegabytes — объём ОЗУ в МБ (НЕ байты): колонка devices.ram — INTEGER,
	// байты переполнили бы её. Единица зафиксирована общим контрактом.
	RAMMegabytes int64
	Disk         string // человекочитаемый общий объём системного диска
	IP           string
	MAC          string
	SerialNumber string
}

// Software — установленное приложение.
type Software struct {
	Name    string
	Version string
}

// Collect собирает железо/ОС/IP. Без списка ПО (он тяжелее — см. InstalledSoftware).
func Collect() DeviceInfo {
	host, _ := os.Hostname()
	ip, mac := NetworkInfo()
	return DeviceInfo{
		Hostname:     host,
		OS:           normalizeOS(runtime.GOOS),
		OSVersion:    osVersion(),
		CPU:          cpuModel(),
		RAMMegabytes: ramMegabytes(),
		Disk:         diskTotal(),
		IP:           ip,
		MAC:          mac,
		SerialNumber: serialNumber(),
	}
}

// InstalledSoftware возвращает список установленного ПО (платформенно).
func InstalledSoftware() []Software {
	return installedSoftware()
}

func LocalIP() string {
	ip, _ := NetworkInfo()
	return ip
}

// netEntry — IPv4-адрес интерфейса вместе с его MAC. Промежуточное представление,
// чтобы логику выбора (selectNetwork) можно было протестировать без реальных
// сетевых интерфейсов машины.
type netEntry struct {
	ip  net.IP
	mac string
}

// selectNetwork выбирает основной адрес устройства из кандидатов: первый
// глобальный/приватный IPv4 с НЕПУСТЫМ MAC. loopback и APIPA link-local
// (169.254.0.0/16) пропускаются всегда — link-local это автоконфиг, когда DHCP не
// ответил, он не маршрутизируется и обычно висит на виртуальном адаптере (Hyper-V,
// WSL, VPN) без MAC. Именно из-за него на Windows в инвентарь попадал ip=169.254.x
// и пустой mac. Кандидат с MAC приоритетнее (реальный сетевой адаптер), даже если в
// списке он идёт после виртуальных; если MAC нет ни у кого — возвращаем первый
// подходящий IP без MAC (лучше настоящего адреса, чем ничего).
func selectNetwork(entries []netEntry) (ip, mac string) {
	var fallbackIP string
	for _, e := range entries {
		ip4 := e.ip.To4()
		if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
			continue
		}
		if e.mac != "" {
			return ip4.String(), e.mac
		}
		if fallbackIP == "" {
			fallbackIP = ip4.String()
		}
	}
	return fallbackIP, ""
}

// NetworkInfo возвращает основной IPv4-адрес устройства и MAC его интерфейса,
// пропуская loopback и APIPA link-local и предпочитая интерфейс с реальным MAC.
func NetworkInfo() (string, string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", ""
	}
	var entries []netEntry
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				entries = append(entries, netEntry{ip: ipnet.IP, mac: iface.HardwareAddr.String()})
			}
		}
	}
	return selectNetwork(entries)
}

// normalizeOS приводит runtime.GOOS к именам из схемы БД (macOS / Windows).
func normalizeOS(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	default:
		return goos
	}
}

// humanBytes форматирует размер в человекочитаемый вид (KB/MB/GB...).
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
