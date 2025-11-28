// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// ErrRelaunchingElevated возвращается если процесс успешно запущен с повышением прав и текущий процесс должен завершиться
	errRelaunchingElevated = errors.New("relaunching elevated")

	shell32           = windows.NewLazySystemDLL("shell32.dll") // Загружает библиотеку shell32 для системных вызовов
	procShellExecuteW = shell32.NewProc("ShellExecuteW")        // Содержит процедуру ShellExecuteW
)

// TokenElevation описывает структуру для получения информации о правах токена
type tokenElevation struct {
	TokenIsElevated uint32 // Состояние повышения прав (0 или 1)
}

// IsAdmin проверяет запущен ли текущий процесс с правами администратора
func isAdmin() (bool, error) {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
		return false, err
	}
	defer tok.Close() // Закрывает токен чтобы избежать утечки ресурсов Windows

	var elev tokenElevation
	var retLen uint32
	if err := windows.GetTokenInformation(
		tok,
		windows.TokenElevation,
		(*byte)(unsafe.Pointer(&elev)),
		uint32(unsafe.Sizeof(elev)),
		&retLen,
	); err != nil {
		return false, err
	}
	return elev.TokenIsElevated != 0, nil
}

// EnsureElevated пытается перезапустить процесс с повышенными правами используя ShellExecuteW и команду "runas"
func ensureElevated() error {
	ok, _ := isAdmin()
	if ok {
		return nil // Прерывает выполнение если права уже повышены
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// Формирует командную строку аргументов с корректным экранированием
	params := buildCommandLine(os.Args[1:])

	r1, _, e1 := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("runas"))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(exe))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(params))),
		0,
		uintptr(1), // SW_SHOWNORMAL чтобы окно было видимым
	)

	// Пускай ShellExecuteW: >32 — успех
	if r1 <= 32 { // Проверяет код возврата ShellExecuteW чтобы определить успешность запуска
		if e1 != nil {
			return e1
		}
		return errors.New("ShellExecuteW(runAs) failed")
	}
	return errRelaunchingElevated
}

// EscapeArgWin экранирует аргумент командной строки в соответствии с правилами Windows (как в CreateProcess)
func escapeArgWin(s string) string {
	if s == "" {
		return `""`
	}

	need := strings.IndexFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '"'
	}) >= 0
	if !need {
		return s
	}

	var b strings.Builder
	b.WriteByte('"')
	bs := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			bs++
		case '"':
			b.WriteString(strings.Repeat("\\", bs*2+1))
			b.WriteByte('"')
			bs = 0
		default:
			if bs > 0 {
				b.WriteString(strings.Repeat("\\", bs))
				bs = 0
			}
			b.WriteByte(c)
		}
	}

	if bs > 0 {
		b.WriteString(strings.Repeat("\\", bs*2))
	}
	b.WriteByte('"')
	return b.String()
}

// BuildCommandLine объединяет список аргументов в одну строку командной строки Windows, применяя экранирование
func buildCommandLine(args []string) string {
	if len(args) == 0 {
		return ""
	}
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = escapeArgWin(a)
	}
	return strings.Join(escaped, " ")
}
