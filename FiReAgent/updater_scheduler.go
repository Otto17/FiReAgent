// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	// Интервалы между проверками обновлений с репозитория
	firstUpdateDelay    = 5 * time.Minute // Первая проверка через 5 минут, после запуска FiReAgent
	dailyUpdateInterval = 24 * time.Hour  // Далее — раз в сутки

	// Флаги CreateProcess
	createBreakawayFromJob uint32 = 0x01000000 // Запускает процесс отдельно от родительского (не завершается при остановке службы)
	createNewProcessGroup  uint32 = 0x00000200 // Создаёт независимую группу процессов (изолирует управляющие сигналы)
)

// StartClientUpdaterScheduler запускает планировщик обновления клиента
func StartClientUpdaterScheduler(mqttSvc *MQTTService) func() {
	stopCh := make(chan struct{})

	go func() {
		// Первая проверка
		timer := time.NewTimer(firstUpdateDelay)
		defer timer.Stop()

		for {
			select {
			case <-stopCh:
				return

			case <-timer.C:
				// Ожидает «окно» без активных операций
				if !waitUntilIdleOrStopped(mqttSvc, stopCh) {
					return
				}
				// Запускает проверку обновлений
				runClientUpdaterOnce(mqttSvc)

				// Все последующие проверки
				timer.Reset(dailyUpdateInterval)
			}
		}
	}()

	// Возвращает функцию остановки планировщика
	return func() {
		select {
		case <-stopCh:
			return
		default:
			close(stopCh)
		}
	}
}

// waitUntilIdleOrStopped блокирует выполнение, пока агент не освободится или не будет остановлен
func waitUntilIdleOrStopped(mqttSvc *MQTTService, stopCh <-chan struct{}) bool {
	const tick = 500 * time.Millisecond // Интервал опроса активных операций, чтобы не вмешиваться в работу агента

	for {
		// Выходит, если агент инициировал остановку
		if mqttSvc != nil && mqttSvc.ops.IsStopping() {
			return false
		}
		// Разрешает запуск обновления при отсутствии активных задач
		if mqttSvc == nil || !mqttSvc.ops.HasActive() {
			return true
		}

		// Иначе ожидает следующей проверки или сигнала остановки
		select {
		case <-stopCh:
			return false
		case <-time.After(tick):
		}
	}
}

// runClientUpdaterOnce запускает модуль утилиты обновления "ClientUpdater.exe" и ждёт его завершения
func runClientUpdaterOnce(mqttSvc *MQTTService) {
	// Не запускается в процессе остановки службы
	if mqttSvc != nil && mqttSvc.ops.IsStopping() {
		return
	}

	// Определяет путь к текущему исполняемому файлу
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("Планировщик обновлений: не удалось определить путь к исполняемому файлу: %v", err)
		return
	}
	updaterPath := filepath.Join(filepath.Dir(exePath), "ClientUpdater.exe")

	if _, err := os.Stat(updaterPath); err != nil {
		log.Printf("Планировщик обновлений: %s не найден: %v", updaterPath, err)
		return
	}

	// Создаёт команду для запуска утилиты обновления
	cmd := exec.Command(updaterPath)

	// Настраивает процесс для автономной работы, отвязывая его от родительского процесса SCM
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createBreakawayFromJob | createNewProcessGroup,
	}

	if err := cmd.Start(); err != nil {
		log.Printf("Планировщик обновлений: не удалось запустить ClientUpdater: %v", err)
		return
	}

	// Запускает updater в фоновом режиме, чтобы избежать блокировки родительского процесса
	// log.Printf("Планировщик обновлений: ClientUpdater запущен (PID %d)", cmd.Process.Pid)
}
