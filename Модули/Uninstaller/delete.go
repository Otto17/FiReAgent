// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// createDeleteProcess создает скрытый процесс cmd.exe, который ждет завершения текущего процесса и удаляет каталоги
func createDeleteProcess(paths ...string) {
	pid := os.Getpid() // PID текущего процесса

	// Собирает команду для удаления нескольких папок
	var delParts []string
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		delParts = append(delParts, fmt.Sprintf(`if exist "%s" rmdir /s /q "%s"`, p, p))
	}

	if len(delParts) == 0 {
		return
	}
	delCmd := strings.Join(delParts, " & ")

	// Ждет завершения текущего процесса, затем удаляет каталоги
	cmdLine := fmt.Sprintf(`cmd /C "for /l %%i in (0,0,1) do (timeout /t 1 /nobreak >nul & tasklist /fi "PID eq %d" | findstr %d >nul || (%s & exit))"`, pid, pid, delCmd)

	startupInfo := &syscall.StartupInfo{}
	startupInfo.Cb = uint32(unsafe.Sizeof(*startupInfo))
	startupInfo.Flags = 0x1    // SW_HIDE
	startupInfo.ShowWindow = 0 // Скрыть окно

	processInfo := &syscall.ProcessInformation{}

	// Преобразует командную строку в UTF-16
	cmdLinePtr, _ := syscall.UTF16PtrFromString(cmdLine)
	const CREATE_NO_WINDOW = 0x08000000

	// Создает дочерний процесс cmd.exe
	syscall.CreateProcess(
		nil,
		cmdLinePtr,
		nil,
		nil,
		false,
		CREATE_NO_WINDOW,
		nil,
		nil,
		startupInfo,
		processInfo,
	)

	// Освобождает дескрипторы процесса и потока
	syscall.CloseHandle(processInfo.Process)
	syscall.CloseHandle(processInfo.Thread)
}
