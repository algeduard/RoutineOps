//go:build linux

package collector

import (
	"bufio"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Linux — полноценная целевая платформа наравне с Windows/macOS.
// Всё железо читается из /proc и /sys, ПО — у пакетного менеджера: без cgo,
// а root нужен только для DMI-серийника (и там есть фолбэк).

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
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return runtime.GOARCH
	}
	return parseCPUModel(string(data))
}

// parseCPUModel достаёт имя процессора из /proc/cpuinfo. Строка "model name" есть
// только на x86: на aarch64 ядро её не печатает вовсе, там имя SoC лежит в
// "Hardware" (или "Model" у Raspberry Pi). Пустой CPU в инвентаре бесполезен,
// поэтому последний рубеж — архитектура.
func parseCPUModel(cpuinfo string) string {
	var hardware, model string
	sc := bufio.NewScanner(strings.NewReader(cpuinfo))
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name":
			return val
		case "Hardware":
			if hardware == "" {
				hardware = val
			}
		case "Model":
			if model == "" {
				model = val
			}
		}
	}
	switch {
	case hardware != "":
		return hardware
	case model != "":
		return model
	}
	return runtime.GOARCH
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

// dmiSerialPath — серийник, выложенный ядром из SMBIOS. Читается без запуска
// dmidecode (которого на минимальных образах просто нет).
const dmiSerialPath = "/sys/class/dmi/id/product_serial"

// placeholderSerials — то, что вписывают в SMBIOS вместо серийника вендоры и
// гипервизоры. Такое значение хуже пустого: оно одинаково на тысячах машин,
// а сервер считает серийник идентификатором железа.
var placeholderSerials = map[string]bool{
	"":                       true,
	"none":                   true,
	"to be filled by o.e.m.": true,
	"system serial number":   true,
	"default string":         true,
	"not specified":          true,
	"0":                      true,
}

func isPlaceholderSerial(s string) bool {
	return placeholderSerials[strings.ToLower(strings.TrimSpace(s))]
}

// parseDmidecode берёт значение из вывода `dmidecode -s`: часть сборок печатает в
// stdout баннер («# dmidecode 3.3») перед самим значением, поэтому берём последнюю
// непустую строку без '#'.
func parseDmidecode(out string) string {
	var val string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		val = line
	}
	return val
}

// serialNumber: сначала sysfs (файл, без сабпроцесса), и только потом dmidecode —
// он требует root И отдельного пакета. Плейсхолдеры отбрасываем на обоих путях,
// иначе весь парк устройств приезжает с серийником "Default string".
func serialNumber() string {
	if data, err := os.ReadFile(dmiSerialPath); err == nil {
		if s := strings.TrimSpace(string(data)); !isPlaceholderSerial(s) {
			return s
		}
	}
	out, err := exec.Command("dmidecode", "-s", "system-serial-number").Output()
	if err != nil {
		return ""
	}
	if s := parseDmidecode(string(out)); !isPlaceholderSerial(s) {
		return s
	}
	return ""
}

// pkgProbes — пакетные менеджеры в порядке проверки PATH. Первый найденный
// выигрывает: на гибридных системах (rpm рядом с apk в контейнере) порядок
// фиксирует, чей ответ считается истиной.
var pkgProbes = []struct {
	bin   string
	args  []string
	parse func(string) []Software
}{
	{"dpkg-query", []string{"-W", "-f=${Package}\t${Version}\n"}, parseDpkg},
	{"rpm", []string{"-qa", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\n"}, parseRpm},
	{"pacman", []string{"-Q"}, parsePacman},
	{"apk", []string{"info", "-v"}, parseApk},
}

// installedSoftware опрашивает пакетный менеджер, который есть в системе.
// Ни одного не нашлось — это не ошибка: инвентаризация ПО просто недоступна
// (Slackware, собранный из исходников образ), железо и ОС всё равно уедут.
func installedSoftware() []Software {
	for _, p := range pkgProbes {
		if _, err := exec.LookPath(p.bin); err != nil {
			continue
		}
		out, err := exec.Command(p.bin, p.args...).Output()
		if err != nil {
			return nil
		}
		return p.parse(string(out))
	}
	return nil
}

// parseTabbed разбирает формат "имя<TAB>версия" — его выдают и dpkg-query, и rpm
// (обоим формат задан флагом, см. pkgProbes). Версия может быть пустой: dpkg
// помнит удалённые, но не вычищенные пакеты.
func parseTabbed(out string) []Software {
	var sw []Software
	for _, line := range strings.Split(out, "\n") {
		name, ver, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if !ok || name == "" {
			continue
		}
		sw = append(sw, Software{Name: name, Version: ver})
	}
	return sw
}

func parseDpkg(out string) []Software { return parseTabbed(out) }
func parseRpm(out string) []Software  { return parseTabbed(out) }

// parsePacman разбирает `pacman -Q`: "имя версия" через пробел. Имя пакета в Arch
// пробелов не содержит, поэтому Fields безопасен.
func parsePacman(out string) []Software {
	var sw []Software
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		s := Software{Name: f[0]}
		if len(f) >= 2 {
			s.Version = f[1]
		}
		sw = append(sw, s)
	}
	return sw
}

// parseApk разбирает `apk info -v`: одно слитное поле "имя-версия-релиз"
// (zlib-1.2.13-r1). Разделителя нет, а дефис легален и внутри имени
// (py3-setuptools-68.0.0-r0), поэтому отрезаем ДВА последних дефиса и требуем,
// чтобы версия начиналась с цифры. Не сошлось — отдаём одно имя: пустая версия
// честнее нарезанного мусора.
func parseApk(out string) []Software {
	var sw []Software
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, ver := splitApkPackage(line)
		sw = append(sw, Software{Name: name, Version: ver})
	}
	return sw
}

func splitApkPackage(pkg string) (name, version string) {
	rel := strings.LastIndexByte(pkg, '-')
	if rel <= 0 {
		return pkg, ""
	}
	ver := strings.LastIndexByte(pkg[:rel], '-')
	if ver <= 0 {
		return pkg, ""
	}
	if v := pkg[ver+1 : rel]; v == "" || v[0] < '0' || v[0] > '9' {
		return pkg, ""
	}
	return pkg[:ver], pkg[ver+1:]
}
