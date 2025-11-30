// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/registry"
)

// updateRegistryVersion обновляет значение DisplayVersion и InstallDate в реестре Windows
func updateRegistryVersion(newVersion string) error {
	const keyPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\FiReAgent`

	// Открывает существующий ключ реестра (в 64-битном разделе)
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return fmt.Errorf("не удалось открыть ключ реестра: %w", err)
	}
	defer k.Close()

	// Обновляет версию (формат "дд.мм.гг.")
	if err := k.SetStringValue("DisplayVersion", newVersion); err != nil {
		return fmt.Errorf("не удалось записать DisplayVersion: %w", err)
	}

	// Обновляет дату установки программы (формат "YYYYMMDD")
	currentDate := time.Now().Format("20060102")
	if err := k.SetStringValue("InstallDate", currentDate); err != nil {
		return fmt.Errorf("не удалось записать InstallDate: %w", err)
	}

	return nil
}
