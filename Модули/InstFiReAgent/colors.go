// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// Цвета вывода сообщений в консоль (по умолчанию цвета отключены)
	ColorBrightWhite  string // Ярко белый
	ColorBrightRed    string // Ярко красный
	ColorBrightGreen  string // Ярко зелёный
	ColorBrightYellow string // Ярко жёлтый
	ColorBrightPurple string // Ярко фиолетовый
	ColorSkyBlue      string // Небесно-голубой
	ColorOrange       string // Оранжевый
	ColorPink         string // Розовый
	ColorTeal         string // Бирюзовый
	ColorBrightBlue   string // Ярко-синий
	ColorReset        string // Сброс цвета
)

// initColors инициализирует ANSI коды, если консоль поддерживает их
func initColors() {
	enable := shouldEnableAnsiColors()
	if enable {
		ColorBrightWhite = "\033[97m"   // Ярко белый
		ColorBrightRed = "\033[91m"     // Ярко красный
		ColorBrightGreen = "\033[92m"   // Ярко зелёный
		ColorBrightYellow = "\033[93m"  // Ярко жёлтый
		ColorBrightPurple = "\033[95m"  // Ярко фиолетовый
		ColorSkyBlue = "\033[38;5;111m" // Небесно-голубой
		ColorOrange = "\033[38;5;208m"  // Оранжевый
		ColorPink = "\033[38;5;213m"    // Розовый
		ColorTeal = "\033[38;5;44m"     // Бирюзовый
		ColorBrightBlue = "\033[94m"    // Ярко-синий
		ColorReset = "\033[0m"          // Сброс цвета
	} else {
		// Все цвета остаются пустыми строками, если ANSI не поддерживается
		ColorBrightWhite = ""
		ColorBrightRed = ""
		ColorBrightGreen = ""
		ColorBrightYellow = ""
		ColorBrightPurple = ""
		ColorSkyBlue = ""
		ColorOrange = ""
		ColorPink = ""
		ColorTeal = ""
		ColorBrightBlue = ""
		ColorReset = ""
	}
}

// shouldEnableAnsiColors определяет, должна ли быть включена поддержка ANSI цветов
func shouldEnableAnsiColors() bool {
	if runtime.GOOS != "windows" {
		return true // Для *nix-платформ ANSI обычно работает по умолчанию
	}

	// Отключает цвета, если версия Windows ниже 10
	if !isWindows10OrGreater() {
		return false
	}

	// Пытается включить режим VT (ANSI) для stdout/stderr
	// Считает, что цвета можно использовать, если режим включен хотя бы для одного потока
	if enableVT(windows.STD_OUTPUT_HANDLE) == nil {
		return true
	}
	if enableVT(windows.STD_ERROR_HANDLE) == nil {
		return true
	}
	return false
}

const enableVirtualTerminalProcessing = 0x0004 // ENABLE_VIRTUAL_TERMINAL_PROCESSING

// enableVT включает режим виртуального терминала для указанного хэндла
func enableVT(stdHandle uint32) error {
	h, err := windows.GetStdHandle(stdHandle)
	if err != nil || h == windows.InvalidHandle {
		return err
	}
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return err
	}
	mode |= enableVirtualTerminalProcessing
	return windows.SetConsoleMode(h, mode)
}

// --- Определение версии Windows через RtlGetVersion

var (
	modntdll          = syscall.NewLazyDLL("ntdll.dll")
	procRtlGetVersion = modntdll.NewProc("RtlGetVersion")
)

// OsVersionInfoExW представляет структуру информации о версии ОС
type osVersionInfoExW struct {
	DwOSVersionInfoSize uint32
	DwMajorVersion      uint32
	DwMinorVersion      uint32
	DwBuildNumber       uint32
	DwPlatformId        uint32
	SzCSDVersion        [128]uint16
	WServicePackMajor   uint16
	WServicePackMinor   uint16
	WSuiteMask          uint16
	WProductType        byte
	WReserved           byte
}

// IsWindows10OrGreater проверяет, является ли текущая ОС Windows 10 или новее
func isWindows10OrGreater() bool {
	var info osVersionInfoExW
	info.DwOSVersionInfoSize = uint32(unsafe.Sizeof(info))
	r1, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&info)))
	if r1 != 0 {
		// Отключает цвета, если не удалось получить версию ОС
		return false
	}
	// Версии ниже 10 не поддерживают ANSI VT без дополнительных библиотек
	return info.DwMajorVersion >= 10
}

// isWindows81OrGreater проверяет, является ли текущая ОС Windows 8.1 или новее
func isWindows81OrGreater() bool {
	var info osVersionInfoExW
	info.DwOSVersionInfoSize = uint32(unsafe.Sizeof(info))
	r1, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&info)))
	if r1 != 0 {
		// Блокирует установку, если не удалось получить версию ОС
		return false
	}
	// Windows 8.1 = 6.3, Windows 10+ = 10.x
	if info.DwMajorVersion > 6 {
		return true
	}
	if info.DwMajorVersion == 6 && info.DwMinorVersion >= 3 {
		return true
	}
	return false
}
