// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"log"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Restart Manager API для определения процессов, блокирующих файл
var (
	rstrtmgr                = windows.NewLazySystemDLL("rstrtmgr.dll")
	procRmStartSession      = rstrtmgr.NewProc("RmStartSession")
	procRmRegisterResources = rstrtmgr.NewProc("RmRegisterResources")
	procRmGetList           = rstrtmgr.NewProc("RmGetList")
	procRmEndSession        = rstrtmgr.NewProc("RmEndSession")
)

const (
	cchReasonSession   = 256 // Максимальная длина строки описания сессии
	rmRebootReasonNone = 0   // Перезагрузка не требуется
)

// rmProcessInfo описывает информацию о процессе из Restart Manager
type rmProcessInfo struct {
	Process struct {
		DwProcessId      uint32
		ProcessStartTime windows.Filetime
	}
	StrAppName      [256]uint16
	StrServiceShort [64]uint16
	ApplicationType uint32
	AppStatus       uint32
	TSSessionId     uint32
	BRestartable    int32
}

// findLockingPIDs возвращает список PID процессов, блокирующих указанный файл
func findLockingPIDs(filePath string) ([]uint32, error) {
	var sessionHandle uint32
	sessionKey := make([]uint16, cchReasonSession+1)

	// Запускает сессию Restart Manager
	ret, _, _ := procRmStartSession.Call(
		uintptr(unsafe.Pointer(&sessionHandle)),
		0,
		uintptr(unsafe.Pointer(&sessionKey[0])),
	)
	if ret != 0 {
		return nil, fmt.Errorf("RmStartSession вернул %d", ret)
	}
	defer procRmEndSession.Call(uintptr(sessionHandle))

	// Регистрирует файл для анализа
	pathPtr, _ := windows.UTF16PtrFromString(filePath)
	ret, _, _ = procRmRegisterResources.Call(
		uintptr(sessionHandle),
		1, // Количество файлов
		uintptr(unsafe.Pointer(&pathPtr)),
		0, 0, 0, 0,
	)
	if ret != 0 {
		return nil, fmt.Errorf("RmRegisterResources вернул %d", ret)
	}

	// Получает список процессов, блокирующих файл
	var nProcInfoNeeded, nProcInfo uint32
	var rebootReasons uint32

	// Первый вызов для определения количества процессов
	ret, _, _ = procRmGetList.Call(
		uintptr(sessionHandle),
		uintptr(unsafe.Pointer(&nProcInfoNeeded)),
		uintptr(unsafe.Pointer(&nProcInfo)),
		0,
		uintptr(unsafe.Pointer(&rebootReasons)),
	)

	if nProcInfoNeeded == 0 {
		return nil, nil // Файл никем не заблокирован
	}

	// Выделяет буфер и получает информацию о процессах
	procInfos := make([]rmProcessInfo, nProcInfoNeeded)
	nProcInfo = nProcInfoNeeded

	ret, _, _ = procRmGetList.Call(
		uintptr(sessionHandle),
		uintptr(unsafe.Pointer(&nProcInfoNeeded)),
		uintptr(unsafe.Pointer(&nProcInfo)),
		uintptr(unsafe.Pointer(&procInfos[0])),
		uintptr(unsafe.Pointer(&rebootReasons)),
	)
	if ret != 0 {
		return nil, fmt.Errorf("RmGetList вернул %d", ret)
	}

	// Собирает PID всех блокирующих процессов
	pids := make([]uint32, 0, nProcInfo)
	for i := uint32(0); i < nProcInfo; i++ {
		pids = append(pids, procInfos[i].Process.DwProcessId)
	}

	return pids, nil
}

// killProcessByPID принудительно завершает процесс по PID
func killProcessByPID(pid uint32) error {
	// Открывает процесс с правом на завершение
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("не удалось открыть процесс PID=%d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)

	// Завершает процесс с кодом выхода 1
	if err := windows.TerminateProcess(handle, 1); err != nil {
		return fmt.Errorf("не удалось завершить процесс PID=%d: %w", pid, err)
	}

	return nil
}

// getProcessName возвращает имя исполняемого файла процесса по PID
func getProcessName(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Sprintf("PID=%d", pid)
	}
	defer windows.CloseHandle(handle)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return fmt.Sprintf("PID=%d", pid)
	}

	// Извлекает только имя файла из полного пути
	fullPath := windows.UTF16ToString(buf[:size])
	for i := len(fullPath) - 1; i >= 0; i-- {
		if fullPath[i] == '\\' || fullPath[i] == '/' {
			return fullPath[i+1:]
		}
	}
	return fullPath
}

// tryUnlockFile пытается разблокировать файл, завершив блокирующие его процессы
// Возвращает true, если файл был разблокирован или не был заблокирован
func tryUnlockFile(filePath string, maxAttempts int) bool {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pids, err := findLockingPIDs(filePath)
		if err != nil {
			log.Printf("Предупреждение: не удалось определить блокирующий процесс: %v", err)
			return false
		}

		if len(pids) == 0 {
			return true // Файл не заблокирован
		}

		// Пытается завершить все блокирующие процессы
		for _, pid := range pids {
			procName := getProcessName(pid)
			log.Printf("Обнаружен блокирующий процесс: %s (PID=%d), попытка %d/%d завершить...",
				procName, pid, attempt, maxAttempts)

			if err := killProcessByPID(pid); err != nil {
				log.Printf("Не удалось завершить %s (PID=%d): %v", procName, pid, err)
			} else {
				log.Printf("Процесс %s (PID=%d) принудительно завершён.", procName, pid)
			}
		}

		// Пауза для освобождения дескрипторов файлов
		time.Sleep(500 * time.Millisecond)

		// Проверяет, разблокирован ли файл
		pidsAfter, _ := findLockingPIDs(filePath)
		if len(pidsAfter) == 0 {
			return true
		}

		if attempt < maxAttempts {
			log.Printf("Файл всё ещё заблокирован, ожидание перед повторной попыткой...")
			time.Sleep(time.Duration(attempt) * time.Second) // Увеличивающаяся пауза
		}
	}

	return false
}
