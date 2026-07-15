//go:build !enterprise

package main

import (
	"fmt"
	"os"
)

// mdm-unseal — офлайн enterprise-инструмент (в open-core недоступен).
// Open-core-сборка даёт заглушку, чтобы `go build ./...` был зелёным.
func main() {
	fmt.Fprintln(os.Stderr, "mdm-unseal — enterprise-инструмент; соберите с -tags enterprise")
	os.Exit(1)
}
