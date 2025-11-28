// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// targetFiles — список исполняемых файлов, процессы которых требуется завершить, если они запущены
var targetFiles = []string{
	"FiReAgent.exe",
	"ModuleCrypto.exe",
	"ModuleInfo.exe",
	"ModuleCommand.exe",
	"ModuleQUIC.exe",
}

// kernel32 — библиотека для работы с процессами Windows
var kernel32 = syscall.NewLazyDLL("kernel32.dll")

// Функции для работы с процессами через kernel32
var (
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	procProcess32NextW           = kernel32.NewProc("Process32NextW")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
	procTerminateProcess         = kernel32.NewProc("TerminateProcess")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImg      = kernel32.NewProc("QueryFullProcessImageNameW")
)

// Константы для работы с процессами
const (
	TH32CS_SNAPPROCESS         = 0x00000002 // Снимок всех процессов
	MAX_PATH                   = 260
	PROCESS_TERMINATE          = 0x0001 // Право завершения процесса
	PROCESS_QUERY_LIMITED_INFO = 0x1000 // Право на чтение информации о процессе
	INVALID_HANDLE_VALUE       = ^uintptr(0)
)

// processEntry32 — структура описания процесса
type processEntry32 struct {
	dwSize              uint32
	cntUsage            uint32
	th32ProcessID       uint32
	th32DefaultHeapID   uintptr
	th32ModuleID        uint32
	cntThreads          uint32
	th32ParentProcessID uint32
	pcPriClassBase      int32
	dwFlags             uint32
	szExeFile           [MAX_PATH]uint16
}

// killBlockingProcesses завершает процессы из списка targetFiles, если они запущены
func killBlockingProcesses(exeDir string) {
	want := make(map[string]struct{})
	for _, f := range targetFiles {
		want[strings.ToLower(filepath.Join(exeDir, f))] = struct{}{}
	}

	// Получение снимка процессов
	hSnap, _, _ := procCreateToolhelp32Snapshot.Call(TH32CS_SNAPPROCESS, 0)
	if hSnap == INVALID_HANDLE_VALUE {
		fmt.Println("Не удалось получить снимок процессов.")
		return
	}
	defer procCloseHandle.Call(hSnap)

	var entry processEntry32
	entry.dwSize = uint32(unsafe.Sizeof(entry))

	ret, _, _ := procProcess32FirstW.Call(hSnap, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		fmt.Println("Process32FirstW ошибка.")
		return
	}

	selfPid := uint32(os.Getpid()) // PID текущего процесса, чтобы не завершить себя

	for {
		pid := entry.th32ProcessID
		if pid != 0 && pid != selfPid {
			hProc, _, _ := procOpenProcess.Call(PROCESS_QUERY_LIMITED_INFO|PROCESS_TERMINATE, 0, uintptr(pid))
			if hProc != 0 {
				full := queryFullImagePath(syscall.Handle(hProc))
				if _, ok := want[strings.ToLower(full)]; ok {
					fmt.Printf("Найден процесс PID=%d путь=%s — завершение...\n", pid, full)
					r1, _, _ := procTerminateProcess.Call(hProc, 1)
					if r1 == 0 {
						fmt.Printf(" Не удалось завершить PID %d\n", pid)
					} else {
						fmt.Println(" Завершен!")
					}
				}
				procCloseHandle.Call(hProc)
			}
		}

		ret, _, _ = procProcess32NextW.Call(hSnap, uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			break
		}
	}

	fmt.Println("Проверка и завершение процессов — готово.")
}

// queryFullImagePath возвращает полный путь к процессу по его handle
func queryFullImagePath(h syscall.Handle) string {
	buf := make([]uint16, MAX_PATH*4)
	size := uint32(len(buf))
	r1, _, _ := procQueryFullProcessImg.Call(
		uintptr(h), 0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r1 == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}

// findPIDsByPath ищет все PID процесса по полному пути к exe (регистр и слэши не важны)
func findPIDsByPath(exePath string) []uint32 {
	want := strings.ToLower(exePath)
	var pids []uint32

	hSnap, _, _ := procCreateToolhelp32Snapshot.Call(TH32CS_SNAPPROCESS, 0)
	if hSnap == INVALID_HANDLE_VALUE {
		return pids
	}
	defer procCloseHandle.Call(hSnap)

	var entry processEntry32
	entry.dwSize = uint32(unsafe.Sizeof(entry))

	ret, _, _ := procProcess32FirstW.Call(hSnap, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		return pids
	}

	selfPid := uint32(os.Getpid())

	for {
		pid := entry.th32ProcessID
		if pid != 0 && pid != selfPid {
			hProc, _, _ := procOpenProcess.Call(PROCESS_QUERY_LIMITED_INFO, 0, uintptr(pid))
			if hProc != 0 {
				full := queryFullImagePath(syscall.Handle(hProc))
				if strings.ToLower(full) == want {
					pids = append(pids, pid)
				}
				procCloseHandle.Call(hProc)
			}
		}
		ret, _, _ = procProcess32NextW.Call(hSnap, uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			break
		}
	}
	return pids
}

// terminatePID завершает процесс по PID, если это возможно
func terminatePID(pid uint32) bool {
	hProc, _, _ := procOpenProcess.Call(PROCESS_TERMINATE, 0, uintptr(pid))
	if hProc == 0 {
		return false
	}
	defer procCloseHandle.Call(hProc)
	r1, _, _ := procTerminateProcess.Call(hProc, 1)
	return r1 != 0
}
