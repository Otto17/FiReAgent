// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/svc"
)

const CurrentVersion = "10.02.26" // Текущая версия AgentMon в формате "дд.мм.гг"

func main() {
	if len(os.Args) > 1 {
		switch strings.ToLower(os.Args[1]) {
		case "-is":
			InstallService()
			return

		case "-sd":
			UninstallService()
			return
		case "--version":
			fmt.Printf("Версия \"AgentMon\": %s\n", CurrentVersion)
			return

		default:
			printUsage()
		}
	} else {
		// Проверяет, запущен ли процесс как служба Windows
		isSvc, _ := svc.IsWindowsService()
		if isSvc {
			RunService()
		} else {
			printUsage()
		}
	}
}

// printUsage выводит справку по использованию программы
func printUsage() {
	fmt.Println("Недопустимая команда. Используйте:")
	fmt.Println("'-is' — установка службы")
	fmt.Println("'-sd' — удаление службы")
	fmt.Println("'--version' — вывод версии программы")
}
