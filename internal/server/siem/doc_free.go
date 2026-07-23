//go:build !enterprise

// Package siem — форвардинг событий аудита в SIEM (enterprise-фича). В open-core пакет
// пуст (build-tag): реальный экспортёр — в exporter.go (//go:build enterprise). Стаб держит
// пакет непустым, чтобы `go build ./...` без -tags enterprise компилировался.
package siem
