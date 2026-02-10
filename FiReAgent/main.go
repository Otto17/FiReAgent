// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const CurrentVersion = "10.02.25" // Текущая версия FiReAgent в формате "дд.мм.гг"

func main() {
	// Устанавливает более агрессивный порог сборщика мусора в 20% (вместо 100% по умолчанию)
	debug.SetGCPercent(20)

	if len(os.Args) > 1 {
		switch strings.ToLower(os.Args[1]) {
		case "-is":
			InstallService()

		case "-sd":
			// Удаляет службу, если установлена
			UninstallService()
			return

		case "--debug":
			// Запускает программу как обычное приложение (для отладки)
			RunAsApplication()

		case "--version":
			fmt.Printf("Версия \"FiReAgent\": %s\n", CurrentVersion)
			return

		default:
			// Выводит подсказку, если нет ключа или он не верный
			fmt.Println("Недопустимая команда. Используйте:")
			fmt.Println("'-is' — установка службы")
			fmt.Println("'-sd' — удаление службы")
			fmt.Println("'--debug' — запуск как приложения")
			fmt.Println("'--version' — вывод версии программы")
		}
	} else {
		// Проверяет, запущен ли процесс как служба Windows
		isSvc, err := svc.IsWindowsService()
		if err != nil {
			fmt.Println("Ошибка определения контекста запуска:", err)
			return
		}
		if isSvc {
			// Запуск в контексте службы
			RunService()
		} else {
			// Выводит подсказку, если нет аргументов и это не служба
			fmt.Println("Недопустимая команда. Используйте:")
			fmt.Println("'-is' — установка службы")
			fmt.Println("'-sd' — удаление службы")
			fmt.Println("'--debug' — запуск как приложения")
			fmt.Println("'--version' — вывод версии программы")
		}
	}
}

// RunAsApplication запускает программу в режиме обычного приложения. В РЕЖИМЕ ОТЛАДКИ АВТООБНОВЛЕНИЯ ОТКЛЮЧЕНЫ (ПЛАНИРОВЩИК НЕ ЗАПУСКАЕТСЯ)
func RunAsApplication() {
	// Обеспечивает запуск только одного экземпляра программы
	release, ok := acquireSingleInstance()
	if !ok {
		fmt.Println("FiReAgent уже запущен!")
		return
	}
	defer release()

	// Проверка на незаполненный config\auth.txt — не запускать программу дальше
	if stop, msg := isAuthTxtIncomplete(); stop {
		logAuthIncompleteOnce() // Разово создаёт запись в логе ModuleCrypto
		fmt.Println(msg)
		return
	}

	// Инициализирует клиент MQTT для обмена данными
	mqttSvc, err := StartMQTTClient()
	if err != nil {
		fmt.Printf("Критическая ошибка: %v\n", err)
		return
	}

	// Настраивает отправители отчетов, используя созданный MQTT клиент
	InitReportSenders(mqttSvc)

	fmt.Println("Запущено как обычное приложение. Для выхода нажмите Enter.")
	fmt.Scanln() // Ожидает ввода пользователя перед завершением работы приложения

	// Дожидается завершения активных операций, потом закрывает MQTT соединение
	fmt.Println("Ожидание завершения активных задач...")
	mqttSvc.DrainActiveOperations(2 * time.Minute)

	// Корректно останавливает MQTT соединение при выходе
	mqttSvc.Stop()
}

// acquireSingleInstance обеспечивает глобальную защиту от дублирующего запуска программы для службы и режима отладки
func acquireSingleInstance() (release func(), ok bool) {
	const mutexName = "Global\\FiReAgent_Lock"

	h, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(mutexName))
	if err != nil {
		// Если мьютекс уже существует, значит, второй запуск
		if err == windows.ERROR_ALREADY_EXISTS {
			// Закрывает полученный хэндл и сообщает, что инстанс уже запущен
			_ = windows.CloseHandle(h)
			return nil, false
		}
		// При любой иной ошибке не даёт запускаться в целях перестраховки
		return nil, false
	}

	// Экземпляр захватил лок и освободит его при завершении
	return func() {
		_ = windows.CloseHandle(h)
	}, true
}

// clearSensitive очищает конфиденциальные данные в ОЗУ после их использования, такие как сертификаты и учетные данные
func clearSensitive(args ...any) {
	for _, arg := range args {
		switch v := arg.(type) {
		case []byte: // Обнуляет байтовый массив для предотвращения утечки
			for i := range v {
				v[i] = 0
			}
		case *tls.Certificate: // Очищает структуру сертификата и приватный ключ
			for i := range v.Certificate {
				for j := range v.Certificate[i] {
					v.Certificate[i][j] = 0
				}
				v.Certificate[i] = nil
			}
			v.PrivateKey = nil
		case *tls.Config: // Очищает сертификаты, хранящиеся в TLS-конфигурации
			if v != nil {
				for i := range v.Certificates {
					clearSensitive(&v.Certificates[i])
				}
				v.Certificates = nil
			}
		}
	}
	runtime.GC() // Вызывает принудительный сбор мусора для немедленной утилизации очищенных объектов
}
