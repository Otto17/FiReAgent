// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// scheduleCleanup запускает скрытый процесс cmd для удаления папки после завершения работы основного процесса и, если требуется, сам файл установщика
func scheduleCleanup(tempDir string, selfDestruct bool) {
	pid := os.Getpid()

	// Формирование команды для удаления временной папки
	cleanCmd := fmt.Sprintf(`rmdir /s /q "%s"`, tempDir)

	// Если нужно самоудаление, добавляет команду удаления исполняемого файла
	if selfDestruct {
		if exePath, err := os.Executable(); err == nil {
			cleanCmd += fmt.Sprintf(` & del /f /q "%s"`, exePath)
		}
	}

	// Ждёт завершения процесса (PID) в цикле (раз в секунду), затем выполняет очистку
	cmdLine := fmt.Sprintf(
		`cmd /C "for /l %%i in (0,0,1) do (timeout /t 1 /nobreak >nul & tasklist /fi "PID eq %d" | findstr %d >nul || (%s & exit))"`,
		pid, pid, cleanCmd,
	)

	// Настраивает параметры для скрытого запуска процесса через WinAPI
	si := &syscall.StartupInfo{Cb: uint32(unsafe.Sizeof(syscall.StartupInfo{})), Flags: 0x1, ShowWindow: 0}
	pi := &syscall.ProcessInformation{}
	cmdLinePtr, _ := syscall.UTF16PtrFromString(cmdLine)

	const CREATE_NO_WINDOW = 0x08000000 // Запускает процесс без создания окна
	syscall.CreateProcess(nil, cmdLinePtr, nil, nil, false, CREATE_NO_WINDOW, nil, nil, si, pi)
	syscall.CloseHandle(pi.Process) // Освобождает ресурсы, не дожидаясь завершения
	syscall.CloseHandle(pi.Thread)
}
