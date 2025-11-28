// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/registry"
)

// registerUninstallEntry Создаёт или обновляет запись в реестре для удаления программы
func registerUninstallEntry(appName, installDir string) error {
	uninstaller := filepath.Join(installDir, "Uninstall.exe")
	displayIcon := uninstaller

	sizeKB, _ := dirSizeKB(installDir) // dirSizeKB Подсчитывает размер папки для поля EstimatedSize

	// Использует WOW64_64KEY для обеспечения совместимости с 64-битными системами
	keyPath := `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + appName
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, keyPath, registry.ALL_ACCESS|registry.WOW64_64KEY)
	if err != nil {
		return fmt.Errorf("CreateKey: %w", err)
	}
	defer k.Close()

	// Строковые значения реестра
	values := map[string]string{
		"DisplayName":     appName,
		"Publisher":       publisher,
		"DisplayVersion":  CurrentVersion,
		"InstallLocation": installDir,
		"UninstallString": fmt.Sprintf(`"%s"`, uninstaller), // Использует кавычки, чтобы путь корректно обрабатывался при запуске
		"DisplayIcon":     displayIcon,
		"InstallDate":     time.Now().Format("20060102"),
	}

	for name, value := range values {
		if err := k.SetStringValue(name, value); err != nil {
			return err
		}
	}

	// DWORD значения
	dwordValues := map[string]uint32{
		"NoModify":      1,
		"NoRepair":      1,
		"EstimatedSize": sizeKB,
	}

	for name, value := range dwordValues {
		if err := k.SetDWordValue(name, value); err != nil {
			return err
		}
	}

	return nil
}

// dirSizeKB Возвращает примерный размер каталога в килобайтах для поля EstimatedSize в реестре
func dirSizeKB(root string) (uint32, error) {
	var total uint64
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		// Продолжает обход, если встретилась ошибка доступа
		if err != nil {
			return nil
		}
		// Пропускает директории
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += uint64(info.Size())
		return nil
	})

	if err != nil {
		return 0, err
	}

	// Вычисляет размер в KB, округляя вверх
	kb := (total + 1023) / 1024

	// Предотвращает переполнение uint32
	if kb > uint64(^uint32(0)) {
		return ^uint32(0), nil
	}
	return uint32(kb), nil
}
