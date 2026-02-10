// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	// Целевая служба для мониторинга
	targetServiceName = "FiReAgent"

	// Путь к утилите обновления
	clientUpdaterPath = `C:\Program Files\FiReAgent\ClientUpdater.exe`

	// Путь к исполняемому файлу FiReAgent
	fiReAgentPath = `C:\Program Files\FiReAgent\FiReAgent.exe`

	// Имя процесса для проверки (без пути)
	clientUpdaterProcess = "ClientUpdater.exe"

	// Интервал проверки состояния службы
	checkInterval = 60 * time.Second

	// Задержка перед первой проверкой (даёт системе время на загрузку)
	initialDelay = 60 * time.Second

	// Флаги CreateProcess для независимого запуска
	createBreakawayFromJob uint32 = 0x01000000
	createNewProcessGroup  uint32 = 0x00000200
)

// watchLoop - основной цикл проверки состояния службы FiReAgent
func watchLoop(stopCh <-chan struct{}) {
	// Небольшая задержка перед первой проверкой
	select {
	case <-stopCh:
		return
	case <-time.After(initialDelay):
	}

	// Первая проверка после начальной задержки
	checkAndRecover()

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			checkAndRecover()
		}
	}
}

// checkAndRecover проверяет состояние службы FiReAgent и запускает ClientUpdater при необходимости
func checkAndRecover() {
	// Сначала проверяет, не запущен ли уже ClientUpdater
	if isProcessRunning(clientUpdaterProcess) {
		return
	}

	state, err := getServiceState(targetServiceName)
	if err != nil {
		// Служба не существует или ошибка доступа — пробует запустить ClientUpdater
		runClientUpdater()
		return
	}

	switch state {
	case svc.Running, svc.StartPending, svc.ContinuePending:
		// Служба работает или запускается — ничего не делает
		return

	case svc.Stopped, svc.StopPending, svc.Paused, svc.PausePending:
		// Служба остановлена или в процессе остановки/паузы — запускает ClientUpdater
		runClientUpdater()
	}
}

// getServiceState возвращает текущее состояние указанной службы
func getServiceState(name string) (svc.State, error) {
	m, err := mgr.Connect()
	if err != nil {
		return 0, err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return 0, err
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return 0, err
	}

	return status.State, nil
}

// runClientUpdater запускает утилиту ClientUpdater, если не удаётся — пробует запустить FiReAgent напрямую
func runClientUpdater() {
	if tryRunClientUpdater() {
		return
	}
	// Fallback: пробует запустить FiReAgent напрямую с ключом -is
	tryRunFiReAgentDirectly()
}

// tryRunClientUpdater пытается запустить ClientUpdater, возвращает true при успехе
func tryRunClientUpdater() bool {
	if _, err := os.Stat(clientUpdaterPath); os.IsNotExist(err) {
		return false
	}

	cmd := exec.Command(clientUpdaterPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createBreakawayFromJob | createNewProcessGroup,
	}

	return cmd.Start() == nil
}

// tryRunFiReAgentDirectly пытается запустить FiReAgent с ключом -is напрямую
func tryRunFiReAgentDirectly() {
	if _, err := os.Stat(fiReAgentPath); os.IsNotExist(err) {
		return
	}

	cmd := exec.Command(fiReAgentPath, "-is")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createBreakawayFromJob | createNewProcessGroup,
	}

	_ = cmd.Start()
}

// isProcessRunning проверяет, запущен ли процесс с указанным именем
func isProcessRunning(processName string) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))

	if err := windows.Process32First(snapshot, &pe); err != nil {
		return false
	}

	for {
		name := windows.UTF16ToString(pe.ExeFile[:])
		if strings.EqualFold(name, processName) {
			return true
		}
		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break
		}
	}

	return false
}
