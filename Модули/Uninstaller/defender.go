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
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// RemoveDefenderExclusion пытается удалить путь из исключений Защитника Windows
func RemoveDefenderExclusion(folder string) error {
	// Ищет командлеты PowerShell для Защитника
	if defenderCmdletsAvailablePS() {
		if err := removeDefenderExclusionPS(folder); err == nil {
			return nil
		}
		// Если PS не справился, пробует через реестр
	}

	// Резервный метод через реестр применяется для Win8.1 или при отключённом Защитнике
	if err := removeDefenderExclusionReg(folder); err != nil {
		return fmt.Errorf("защитник Windows недоступен/отключён; не удалось удалить исключение через реестр: %w", err)
	}
	return nil
}

// defenderCmdletsAvailablePS проверяет, доступны ли командлеты Защитника Windows в PowerShell
func defenderCmdletsAvailablePS() bool {
	ps := `$ErrorActionPreference='SilentlyContinue'; if (Get-Command -Name Remove-MpPreference) { 'yes' } else { 'no' }`
	out, _ := exec.Command(powerShellExe(),
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", ps).CombinedOutput()
	return strings.Contains(strings.ToLower(string(out)), "yes")
}

// removeDefenderExclusionPS удаляет исключение с помощью командлета Remove-MpPreference в PowerShell
func removeDefenderExclusionPS(folder string) error {
	cmd := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; $p='%s'; if (Get-Command -Name Remove-MpPreference) { try { $mp=Get-MpPreference; if ($mp -and ($mp.ExclusionPath -contains $p)) { Remove-MpPreference -ExclusionPath $p } } catch { throw $_ } } else { throw 'Defender cmdlets unavailable' }`,
		escapePSSingleQuotes(folder),
	)
	out, err := exec.Command(
		powerShellExe(),
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", cmd,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("PowerShell Remove-MpPreference: %v; out: %s",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeDefenderExclusionReg удаляет исключение из реестра Windows Defender, если PowerShell недоступен
func removeDefenderExclusionReg(folder string) error {
	keyPath := `SOFTWARE\Microsoft\Windows Defender\Exclusions\Paths`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.SET_VALUE|registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		// Возвращает nil, если ключ реестра не существует
		if errors.Is(err, registry.ErrNotExist) || errors.Is(err, syscall.ENOENT) {
			return nil
		}
		return err
	}
	defer k.Close()

	// Удаляет исключения, пробуя несколько вариантов пути (со слешем и без)
	candidates := uniqueNonEmpty([]string{
		folder,
		strings.TrimRight(folder, `\`),
		strings.TrimRight(folder, `\`) + `\`,
	})
	var lastErr error
	var anyDeleted bool
	for _, name := range candidates {
		if err := k.DeleteValue(name); err != nil {
			if errors.Is(err, registry.ErrNotExist) {
				lastErr = nil // Не страшно, просто нет значения с таким именем
				continue
			}
			lastErr = err
			continue
		}
		anyDeleted = true
	}
	if anyDeleted || lastErr == nil {
		return nil
	}
	return lastErr
}

// uniqueNonEmpty удаляет дубликаты и пустые строки из входного слайса
func uniqueNonEmpty(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// escapePSSingleQuotes экранирует одинарные кавычки для использования в командной строке PowerShell
func escapePSSingleQuotes(s string) string {
	return strings.ReplaceAll(s, `'`, `''`)
}

// powerShellExe определяет полный путь к исполняемому файлу powershell.exe
func powerShellExe() string {
	// Ищет 64-битную версию PowerShell
	win := os.Getenv("SystemRoot")
	p := filepath.Join(win, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "powershell.exe" // Иначе использует PATH
}
