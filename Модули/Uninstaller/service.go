// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

// advapi32 — библиотека для работы со службами Windows
var advapi32 = syscall.NewLazyDLL("Advapi32.dll")

// Функции управления службами Windows
var (
	procOpenSCManagerW       = advapi32.NewProc("OpenSCManagerW")       // Открывает диспетчер служб
	procOpenServiceW         = advapi32.NewProc("OpenServiceW")         // Открывает конкретную службу
	procQueryServiceStatusEx = advapi32.NewProc("QueryServiceStatusEx") // Получает расширенный статус службы
	procCloseServiceHandle   = advapi32.NewProc("CloseServiceHandle")   // Закрывает дескриптор службы/SCM
)

// Константы для Service Control Manager
const (
	SC_MANAGER_CONNECT     = 0x0001     // Право на подключения к SCM
	SERVICE_QUERY_STATUS   = 0x0004     // Право на запроса статуса службы
	SC_STATUS_PROCESS_INFO = 0          // Тип структуры статуса
	SERVICE_STOPPED        = 0x00000001 // Служба остановлена
	SERVICE_START_PENDING  = 0x00000002 // Идёт запуск службы
	SERVICE_STOP_PENDING   = 0x00000003 // Идёт остановка службы
	SERVICE_RUNNING        = 0x00000004 // Служба работает

	ERROR_SERVICE_DOES_NOT_EXIST = syscall.Errno(1060) // Служба не найдена
)

// serviceStatusProcess — структура для получения информации о службе
type serviceStatusProcess struct {
	dwServiceType             uint32 // Тип службы
	dwCurrentState            uint32 // Текущее состояние (RUNNING, STOPPED и т.д.)
	dwControlsAccepted        uint32 // Принимаемые команды
	dwWin32ExitCode           uint32 // Код выхода
	dwServiceSpecificExitCode uint32 // Служебный код
	dwCheckPoint              uint32 // Контрольный счётчик прогресса
	dwWaitHint                uint32 // Рекомендуемое время ожидания
	dwProcessId               uint32 // PID процесса службы
	dwServiceFlags            uint32 // Флаги службы
}

// serviceExistsAndRunning проверяет наличие службы и возвращает её состояние (работает или нет)
func serviceExistsAndRunning(name string) (exists bool, running bool, err error) {
	hSC, _, err := procOpenSCManagerW.Call(0, 0, SC_MANAGER_CONNECT)
	if hSC == 0 {
		return false, false, err
	}
	defer procCloseServiceHandle.Call(hSC)

	name16, _ := syscall.UTF16PtrFromString(name)
	hSvc, _, err2 := procOpenServiceW.Call(hSC, uintptr(unsafe.Pointer(name16)), SERVICE_QUERY_STATUS)
	if hSvc == 0 {
		if errno, ok := err2.(syscall.Errno); ok && errno == ERROR_SERVICE_DOES_NOT_EXIST {
			return false, false, nil
		}
		return false, false, err2
	}
	defer procCloseServiceHandle.Call(hSvc)

	var status serviceStatusProcess
	var needed uint32
	r1, _, err3 := procQueryServiceStatusEx.Call(
		hSvc,
		uintptr(SC_STATUS_PROCESS_INFO),
		uintptr(unsafe.Pointer(&status)),
		uintptr(uint32(unsafe.Sizeof(status))),
		uintptr(unsafe.Pointer(&needed)),
	)
	if r1 == 0 {
		return true, false, err3
	}
	return true, status.dwCurrentState == SERVICE_RUNNING, nil
}

// stopAndDeleteServiceViaExe запускает "FiReAgent.exe -sd", чтобы остановить и удалить службу через FiReAgent.exe
func stopAndDeleteServiceViaExe(exeDir string) error {
	fireAgentExe := filepath.Join(exeDir, "FiReAgent.exe")
	if _, err := os.Stat(fireAgentExe); err != nil {
		fmt.Println("FiReAgent.exe не найден в текущей папке, пропуск запуска с ключом '-sd'")
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // Ограничение выполнения "FiReAgent.exe -sd" одной минутой
	defer cancel()

	cmd := exec.CommandContext(ctx, fireAgentExe, "-sd") // Запуск с ключом -sd
	cmd.Dir = exeDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} // Скрыть окно
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		fmt.Println("Таймаут выполнения \"FiReAgent.exe -sd\"")
		return errors.New("таймаут выполнения FiReAgent.exe -sd")
	}
	if err != nil {
		fmt.Println("Ошибка при выполнении \"FiReAgent.exe -sd\":", err)
	}
	return err
}

// queryServiceState — возвращает информацию, существует ли служба и её текущее состояние (RUNNING, STOPPED и т.д.)
func queryServiceState(name string) (exists bool, state uint32, err error) {
	// Подключается к SCM
	hSC, _, err := procOpenSCManagerW.Call(0, 0, SC_MANAGER_CONNECT)
	if hSC == 0 {
		return false, 0, err
	}
	defer procCloseServiceHandle.Call(hSC)

	// Открывает нужную службу по имени
	name16, _ := syscall.UTF16PtrFromString(name)
	hSvc, _, err2 := procOpenServiceW.Call(hSC, uintptr(unsafe.Pointer(name16)), SERVICE_QUERY_STATUS)
	if hSvc == 0 {
		// Если службы нет — аккуратно выходит
		if errno, ok := err2.(syscall.Errno); ok && errno == ERROR_SERVICE_DOES_NOT_EXIST {
			return false, 0, nil
		}
		return false, 0, err2
	}
	defer procCloseServiceHandle.Call(hSvc)

	// Запрашивает расширенный статус через QueryServiceStatusEx
	var status serviceStatusProcess
	var needed uint32
	r1, _, err3 := procQueryServiceStatusEx.Call(
		hSvc,
		uintptr(SC_STATUS_PROCESS_INFO),
		uintptr(unsafe.Pointer(&status)),
		uintptr(uint32(unsafe.Sizeof(status))),
		uintptr(unsafe.Pointer(&needed)),
	)
	if r1 == 0 {
		return true, 0, err3 // Ошибка запроса
	}

	// Возвращает флаг, состояние и nil
	return true, status.dwCurrentState, nil
}
