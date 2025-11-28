// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// scheduleTempDelete запускает скрытый процесс cmd для удаления папки после завершения работы основного процесса
func scheduleTempDelete(path string) {
	pid := os.Getpid()
	// Использует цикл for /l для многократной проверки PID и удаления папки после выхода основного процесса
	cmdLine := fmt.Sprintf(
		`cmd /C "for /l %%i in (1,1,10) do (timeout /t 1 /nobreak >nul & tasklist /fi "PID eq %d" | findstr %d >nul || (rmdir /s /q "%s" & exit))"`,
		pid, pid, path,
	)

	si := &syscall.StartupInfo{Cb: uint32(unsafe.Sizeof(syscall.StartupInfo{})), Flags: 0x1, ShowWindow: 0}
	pi := &syscall.ProcessInformation{}
	cmdLinePtr, _ := syscall.UTF16PtrFromString(cmdLine)

	const CREATE_NO_WINDOW = 0x08000000 // Запускает процесс без создания окна
	syscall.CreateProcess(nil, cmdLinePtr, nil, nil, false, CREATE_NO_WINDOW, nil, nil, si, pi)
	syscall.CloseHandle(pi.Process) // Освобождает ресурсы, не дожидаясь завершения
	syscall.CloseHandle(pi.Thread)
}
