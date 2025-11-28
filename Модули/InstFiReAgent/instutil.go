// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed tool/7z.exe tool/7z.dll tool/installation.7z
var content embed.FS // Content содержит внедренные файлы, необходимые для установки

// Extract7z распаковывает архив с помощью утилиты 7-Zip
func extract7z(sevenZipExe, archivePath, destDir string) error {
	args := []string{
		"x",
		archivePath,
		fmt.Sprintf("-o%s", destDir),
		"-y",
	}
	cmd := exec.Command(sevenZipExe, args...)

	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code := ee.ExitCode()
			// Проверяет коды выхода 0 (успех) и 1 (предупреждения) для корректной обработки
			if code == 0 || code == 1 {
				return nil
			}
			return fmt.Errorf("7-Zip завершился с кодом %d", code)
		}
		return err
	}
	return nil
}

// FlattenSingleTopDir перемещает содержимое единственной верхней папки в корневой каталог targetDir
func flattenSingleTopDir(targetDir string) error {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return err
	}

	var topDirs []os.DirEntry
	var hasFiles bool

	for _, e := range entries {
		if e.IsDir() {
			topDirs = append(topDirs, e)
		} else {
			hasFiles = true
		}
	}

	// Продолжает только если нет файлов в корне и присутствует ровно одна папка-обертка
	if hasFiles || len(topDirs) != 1 {
		return nil
	}

	wrapper := filepath.Join(targetDir, topDirs[0].Name())
	inner, err := os.ReadDir(wrapper)
	if err != nil {
		return err
	}

	for _, child := range inner {
		oldPath := filepath.Join(wrapper, child.Name())
		newPath := filepath.Join(targetDir, child.Name())

		// Удаляет существующий элемент перед перемещением, чтобы избежать ошибок
		if _, err := os.Stat(newPath); err == nil {
			_ = os.RemoveAll(newPath)
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("перемещение %s -> %s: %w", oldPath, newPath, err)
		}
	}

	// Удаляет пустую оболочку
	return os.Remove(wrapper)
}
