// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// uninstallRequest представляет команду для запуска самоудаления: {"Uninstall": "<mqttID>"}
type uninstallRequest struct {
	Uninstall string `json:"Uninstall"`
}

// processUninstallMessage запускает самоудаление при совпадении ID
func processUninstallMessage(mqttSvc *MQTTService, payload []byte) error {
	var req uninstallRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		// Не считает это ошибкой обработки, просто фиксирует невалидный JSON в логе
		log.Printf("Получена некорректная команда деинсталляции (невалидный JSON): %v", err)
		return nil
	}

	if req.Uninstall == "" {
		log.Printf("Получена команда деинсталляции без ID")
		return nil
	}

	if req.Uninstall != mqttSvc.mqttID {
		// Логирует неудачную попытку, если ID не совпадает
		log.Printf("Неудачная попытка деинсталляции с ID: %q", req.Uninstall)
		return nil
	}

	// Запускает деинсталлятор, если ID совпадает
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("не удалось определить путь к исполняемому файлу: %v", err)
	}
	uninstallerPath := filepath.Join(filepath.Dir(exePath), "Uninstall.exe")

	if _, err := os.Stat(uninstallerPath); err != nil {
		return fmt.Errorf("не найден деинсталлятор: %s: %v", uninstallerPath, err)
	}

	cmd := exec.Command(uninstallerPath, "--force")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("не удалось запустить деинсталлятор: %v", err)
	}

	// Логирует успешный запуск деинсталлятора
	// log.Printf("Принята команда деинсталляции для ID: %q. Запущено: %s --force (PID %d)", req.Uninstall, uninstallerPath, cmd.Process.Pid)

	return nil
}
