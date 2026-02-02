// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	PATH_LOG      = "./log" // Путь сохранения лог-файла
	MAX_LOG_SIZE  = 1000000 // Максимальный размер лог-файла в байтах для ротации (Установлен 1 Мбайт)
	MAX_LOG_FILES = 2       // Максимальное количество архивных лог-файлов для хранения
)

// WriteToLogFile записывает форматированное сообщение в лог-файл и выполняет ротацию
func WriteToLogFile(format string, args ...any) {
	// Создание директории необходимо для сохранения лог-файлов
	if _, err := os.Stat(PATH_LOG); os.IsNotExist(err) {
		err := os.MkdirAll(PATH_LOG, os.ModePerm)
		if err != nil {
			return
		}
	}

	logFilePath := filepath.Join(PATH_LOG, "log_ModuleQUIC.txt")

	// Проверка размера файла для выполнения ротации при превышении лимита
	if info, err := os.Stat(logFilePath); err == nil && info.Size() >= MAX_LOG_SIZE {
		RotateLogFiles()
	}

	// Использует O_APPEND и O_CREATE для добавления данных и создания файла при необходимости
	file, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer file.Close()

	message := fmt.Sprintf(format, args...)

	logMessage := fmt.Sprintf("%s: %s\n", time.Now().Format("02.01.06г. в 15:04:05"), message)

	_, err = file.WriteString(logMessage)
	if err != nil {
		return
	}
}

// RotateLogFiles выполняет процесс ротации лог-файлов
func RotateLogFiles() {
	// Удаление самого старого архива для соблюдения лимита MAX_LOG_FILES
	oldestLogFile := filepath.Join(PATH_LOG, fmt.Sprintf("log_ModuleQUIC_%d.txt", MAX_LOG_FILES-1))
	if _, err := os.Stat(oldestLogFile); err == nil {
		os.Remove(oldestLogFile)
	}

	// Сдвиг всех существующих архивов для освобождения места под новый архив (_0)
	for i := MAX_LOG_FILES - 1; i > 0; i-- {
		oldFile := filepath.Join(PATH_LOG, fmt.Sprintf("log_ModuleQUIC_%d.txt", i-1))
		newFile := filepath.Join(PATH_LOG, fmt.Sprintf("log_ModuleQUIC_%d.txt", i))
		if _, err := os.Stat(oldFile); err == nil {
			os.Rename(oldFile, newFile)
		}
	}

	// Текущий файл становится новым архивом _0
	currentLogFile := filepath.Join(PATH_LOG, "log_ModuleQUIC.txt")
	newLogFile := filepath.Join(PATH_LOG, "log_ModuleQUIC_0.txt")
	if _, err := os.Stat(currentLogFile); err == nil {
		os.Rename(currentLogFile, newLogFile)
	}
}
