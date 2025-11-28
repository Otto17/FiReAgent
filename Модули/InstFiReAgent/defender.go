// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureDefenderExclusion добавляет указанную папку в исключения Защитника Windows, если он активен
func EnsureDefenderExclusion(folder string) error {
	// Создает папку, если она еще не существует
	if _, err := os.Stat(folder); os.IsNotExist(err) {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return fmt.Errorf("не удалось создать %s: %v", folder, err)
		}
	}

	// Проверяет, находится ли путь уже в списке исключений
	exists, _ := isPathExcludedByDefenderPS(folder)
	if exists {
		return nil
	}

	// Пытается добавить исключение
	if err := addDefenderExclusionPS(folder); err != nil {
		return fmt.Errorf("в Defender исключение не поддерживается: %w", err)
	}

	// Проверяет, что исключение успешно добавлено
	ok, _ := isPathExcludedByDefenderPS(folder)
	if !ok {
		return fmt.Errorf("путь не появился в списке исключений после Add-MpPreference")
	}
	return nil
}

// isPathExcludedByDefenderPS проверяет наличие пути в исключениях Защитника с помощью PowerShell
func isPathExcludedByDefenderPS(folder string) (bool, error) {
	// Скрипт получает список исключений ExclusionPath из Get-MpPreference
	ps := `$ErrorActionPreference='Stop'; $p=$null; try{$p=(Get-MpPreference).ExclusionPath}catch{}; if($null -eq $p){''} else { $p -join [Environment]::NewLine }`
	out, err := exec.Command(powerShellExe(),
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", ps).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("PowerShell Get-MpPreference: %v; out: %s", err, strings.TrimSpace(string(out)))
	}

	target := normalizeDefenderPath(folder)
	lines := strings.Split(string(out), "\n")
	for _, raw := range lines {
		n := normalizeDefenderPath(raw)
		if n == "" {
			continue
		}
		if coversExclusion(n, target) {
			return true, nil
		}
	}
	return false, nil
}

// addDefenderExclusionPS добавляет путь в исключения Защитника через PowerShell
func addDefenderExclusionPS(folder string) error {
	cmd := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; Add-MpPreference -ExclusionPath '%s'`,
		escapePSSingleQuotes(folder),
	)
	out, err := exec.Command(
		powerShellExe(),
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", cmd,
	).CombinedOutput()
	if err != nil {
		// Возвращает короткое, дружелюбное сообщение об ошибке
		return errors.New(summarizeDefenderPSFailure(out, err))
	}
	return nil
}

// summarizeDefenderPSFailure преобразует вывод PowerShell в короткое понятное сообщение
func summarizeDefenderPSFailure(out []byte, err error) string {
	s := strings.ToLower(string(out))

	// Ищет типовые ошибки даже в локализованных версиях Windows
	switch {
	case strings.Contains(s, "couldnotautoloadmatchingmodule") ||
		strings.Contains(s, "import-module defender") ||
		strings.Contains(s, "the term 'add-mppreference' is not recognized") ||
		strings.Contains(s, "is not recognized"):
		return "Windows Defender недоступен (модуль Defender отсутствует или отключён)"

	case strings.Contains(s, "access is denied") ||
		strings.Contains(s, "access denied") ||
		strings.Contains(s, "отказано в доступе"):
		return "недостаточно прав (запустите установщик от имени администратора)"

	case strings.Contains(s, "not supported") ||
		strings.Contains(s, "не поддерживает") ||
		strings.Contains(s, "операция не поддерживается"):
		return "операция не поддерживается на этой версии Windows"

	case errors.Is(err, exec.ErrNotFound):
		return "PowerShell не найден в системе"
	}

	// Фоллбек: без подробностей, максимально коротко
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Sprintf("ошибка PowerShell (код %d)", ee.ExitCode())
	}
	return "не удалось добавить исключение"
}

// escapePSSingleQuotes экранирует одинарные кавычки для использования в строках PowerShell
func escapePSSingleQuotes(s string) string {
	return strings.ReplaceAll(s, `'`, `''`)
}

// powerShellExe возвращает путь к 64-битному исполняемому файлу PowerShell
func powerShellExe() string {
	win := os.Getenv("SystemRoot")
	p := filepath.Join(win, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "powershell.exe"
}

// normalizeDefenderPath нормализует путь для сравнения с исключениями Защитника
func normalizeDefenderPath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, `/`, `\`)
	s = strings.TrimSuffix(s, `\`)
	return strings.ToLower(s)
}

// coversExclusion проверяет, покрывает ли исключение целевой путь
func coversExclusion(exclName, normalizedTarget string) bool {
	e := normalizeDefenderPath(exclName)
	if e == "" {
		return false
	}
	// Добавляет слеш, чтобы гарантировать, что исключение 'C:\Dir' не совпадет с 'C:\DirFile'
	if !strings.HasSuffix(e, `\`) {
		e += `\`
	}
	t := normalizedTarget
	if !strings.HasSuffix(t, `\`) {
		t += `\`
	}
	return strings.HasPrefix(t, e)
}
