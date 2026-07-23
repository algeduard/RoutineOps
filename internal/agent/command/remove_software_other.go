//go:build !windows

package command

import (
	"context"
	"fmt"
)

// removeSoftware — удаление ПО реализовано только под Windows (реестр Uninstall +
// деинсталлятор). На прочих ОС возвращаем ошибку: сервер получит ERROR-результат и
// покажет его администратору (менеджеры пакетов apt/rpm/brew — отдельная будущая работа).
func removeSoftware(_ context.Context, name, _ string) (string, error) {
	return "", fmt.Errorf("удаление ПО пока не поддерживается на этой ОС (%q)", name)
}
