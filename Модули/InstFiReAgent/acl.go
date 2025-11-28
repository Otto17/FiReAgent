// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"os/exec"
)

// EnsureAclFullControl устанавливает полные права (Full Control) для указанного пути с помощью icacls
func ensureAclFullControl(path string) error {
	// Включает наследование прав, игнорируя возможные ошибки
	_ = exec.Command("icacls", path, "/inheritance:e").Run()

	// Устанавливает полные права для основных системных групп:
	// (OI)(CI)F - Full control с наследованием
	// /T - рекурсивно
	// /C - продолжает при ошибках
	// /Q - тихий режим
	cmd := exec.Command("icacls", path, "/grant",
		"*S-1-5-18:(OI)(CI)F",     // СИСТЕМА
		"*S-1-5-32-544:(OI)(CI)F", // Администраторы
		"*S-1-5-32-545:(OI)(CI)F", // Пользователи
		"/T", "/C", "/Q",
	)
	return cmd.Run()
}
