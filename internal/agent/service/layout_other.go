//go:build !darwin && !linux

package service

// InstallLayout — на Windows установку выполняет MSI (WiX кладёт бинарь в Program
// Files, серты — рядом, флаги фиксированы в installer). Перекладка из кода не
// нужна и сломала бы рабочий MSI-поток — Relocate=false, пути пустые.
func InstallLayout() Layout {
	return Layout{Relocate: false}
}
