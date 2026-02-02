// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// EnsureDefenderExclusion проверяет и добавляет путь в исключения, если необходимо
func EnsureDefenderExclusion(folder string) error {
	WriteToLogFile("[Defender] Проверяем/добавляем исключение: %s", folder)
	// Проверка через реестр
	excluded, regErr := isPathExcludedByDefenderRegistry(folder)
	if regErr != nil {
		WriteToLogFile("[Defender] Проверка через реестр не удалась: %v", regErr)
		// Если реестр не дал результат — fallback через PowerShell
		psExcluded, psErr := isPathExcludedByDefenderPS(folder)
		if psErr != nil {
			WriteToLogFile("[Defender] Проверка через PowerShell не удалась: %v", psErr)
		} else if psExcluded {
			WriteToLogFile("[Defender] Уже в исключениях (определено PowerShell): %s", folder)
			return nil
		}
	} else if excluded {
		WriteToLogFile("[Defender] Уже в исключениях (определено через реестр): %s", folder)
		return nil
	}

	// Добавление через PowerShell
	WriteToLogFile("[Defender] Добавляем путь через PowerShell: %s", folder)
	if err := addDefenderExclusionPS(folder); err != nil {
		return fmt.Errorf("не удалось добавить путь (%s) в исключения Defender через PowerShell: %v", folder, err)
	}

	WriteToLogFile("[Defender] Путь добавлен через PowerShell: %s", folder)
	return nil
}

// removeDefenderExclusionPS удаляет путь из исключений Defender через PowerShell
func removeDefenderExclusionPS(folder string) error {
	cmd := fmt.Sprintf(`$ErrorActionPreference='Stop'; Remove-MpPreference -ExclusionPath '%s'`, escapePSSingleQuotes(folder))
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", cmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("PowerShell Remove-MpPreference: %v; out: %s", err, trimOut(out))
	}
	WriteToLogFile("[Defender] Исключение удалено через PowerShell: %s", folder)
	return nil
}

// isPathExcludedByDefenderRegistry проверяет наличие пути в реестре исключений Defender
func isPathExcludedByDefenderRegistry(folder string) (bool, error) {
	const prefKey = `SOFTWARE\Microsoft\Windows Defender\Exclusions\Paths`
	const policyKey = `SOFTWARE\Policies\Microsoft\Windows Defender\Exclusions\Paths`

	target := normalizeDefenderPath(folder)
	keys := []string{policyKey, prefKey}

	var anyOpened bool
	var firstErr error

	for _, kPath := range keys {
		// Открывает ключ с приоритетом для 64-битного вида
		k, err := openKey64(registry.LOCAL_MACHINE, kPath, registry.QUERY_VALUE)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		anyOpened = true

		names, err := k.ReadValueNames(-1)
		k.Close()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		// Если исключение покрывает путь — значит в списке
		for _, name := range names {
			if coversExclusion(name, target) {
				return true, nil
			}
		}
	}

	if !anyOpened && firstErr != nil {
		return false, firstErr
	}
	return false, nil
}

// openKey64 открывает ключ реестра в 64-битном режиме
func openKey64(base registry.Key, path string, access uint32) (registry.Key, error) {
	return registry.OpenKey(base, path, access)
}

// coversExclusion определяет, покрывает ли исключение указанный путь
func coversExclusion(exclName, normalizedTarget string) bool {
	e := normalizeDefenderPath(exclName)
	if e == "" {
		return false
	}
	if !strings.HasSuffix(e, `\`) {
		e += `\`
	}
	t := normalizedTarget
	if !strings.HasSuffix(t, `\`) {
		t += `\`
	}
	return strings.HasPrefix(t, e)
}

// normalizeDefenderPath нормализует путь для сравнения с исключениями Defender
func normalizeDefenderPath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, `/`, `\`)
	s = strings.TrimSuffix(s, `\`)
	return strings.ToLower(s)
}

// isPathExcludedByDefenderPS проверяет наличие пути в исключениях Defender через PowerShell
func isPathExcludedByDefenderPS(folder string) (bool, error) {
	ps := `$ErrorActionPreference='Stop'; $p=(Get-MpPreference).ExclusionPath; if($null -eq $p){''} else { $p -join [Environment]::NewLine }`
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", ps).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("PowerShell Get-MpPreference: %v; out: %s", err, trimOut(out))
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

// addDefenderExclusionPS добавляет путь в исключения Defender через PowerShell
func addDefenderExclusionPS(folder string) error {
	cmd := fmt.Sprintf(`$ErrorActionPreference='Stop'; Add-MpPreference -ExclusionPath '%s'`, escapePSSingleQuotes(folder))
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", cmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("PowerShell Add-MpPreference: %v; out: %s", err, trimOut(out))
	}
	return nil
}

// escapePSSingleQuotes экранирует одинарные кавычки в строке для использования в PowerShell
func escapePSSingleQuotes(s string) string {
	return strings.ReplaceAll(s, `'`, `''`)
}

// trimOut обрезает вывод PowerShell до заданной длины
func trimOut(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 2000 {
		s = s[:2000] + "..."
	}
	return s
}
