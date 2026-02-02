// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows/registry"
)

// PipeMessageType определяет тип сообщения для канала
type PipeMessageType int

const (
	PipeMessageData PipeMessageType = iota
	PipeMessageResponse
)

// PipeMessage представляет сообщение, передаваемое через канал
type PipeMessage struct {
	Type PipeMessageType
	Data []byte
}

// StartModuleAndConnect запускает модуль и подключается к именованному каналу
func StartModuleAndConnect(moduleName, pipeGUID, baseTimeHex string, mode ...string) (net.Conn, error) {
	// Определяет путь к запускаемому модулю
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("не удалось получить путь к модулю: %v", err)
	}
	modulePath := filepath.Join(filepath.Dir(exePath), moduleName)

	// log.Printf("Запуск модуля: %s с режимом %s", moduleName, mode)

	// Формирует аргументы: BaseTime, режим (full/cert, если он есть), и опциональные флаги
	argsNP := []string{baseTimeHex}

	// Добавляет режим, если он указан
	if len(mode) > 0 && mode[0] != "" {
		argsNP = append(argsNP, mode[0])
		// log.Printf("Режим: %s", mode[0])
	}

	// Добавляет остальные аргументы
	argsNP = append(argsNP, "--pipe", "--pipename="+pipeGUID)

	cmd := exec.Command(modulePath, argsNP...)
	cmd.Dir = filepath.Dir(modulePath) // Установка рабочей директории для корректной записи логов
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("не удалось запустить модуль '%s': %v", moduleName, err)
	}

	// Даёт время модулю ModuleCrypto.exe, чтобы он успел создать NamedPipeServerStream
	time.Sleep(100 * time.Millisecond)

	// Подключается к именному каналу
	pipeName := `\\.\pipe\` + pipeGUID
	// log.Printf("Ожидание подключения к каналу: %s", pipeName)

	var conn net.Conn
	maxWait := 35 * time.Second
	startTime := time.Now()
	for {
		conn, err = winio.DialPipe(pipeName, nil)
		if err == nil {
			// log.Printf("Канал подключен: %s", pipeName)
			break
		}
		if time.Since(startTime) > maxWait {
			return nil, fmt.Errorf("таймаут подключения к каналу: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return conn, nil
}

// SendPipeData отправляет бинарные данные через канал с префиксом длины
func SendPipeData(conn net.Conn, data []byte) error {
	length := int32(len(data))
	if err := binary.Write(conn, binary.LittleEndian, length); err != nil {
		return fmt.Errorf("ошибка записи длины данных: %v", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("ошибка записи данных: %v", err)
	}
	return nil
}

// ReadPipeData читает бинарные данные из канала с префиксом длины
func ReadPipeData(conn io.Reader) ([]byte, error) {
	var length int32
	if err := binary.Read(conn, binary.LittleEndian, &length); err != nil {
		return nil, fmt.Errorf("ошибка чтения длины данных: %v", err)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("ошибка чтения данных: %v", err)
	}
	return data, nil
}

// GetBaseTimeHex получает значение BaseTime из реестра и переводит его в HEX
func GetBaseTimeHex() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters`, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("не удалось открыть реестр: %w", err)
	}
	defer key.Close()

	baseTime, _, err := key.GetIntegerValue("BaseTime")
	if err != nil {
		return "", fmt.Errorf("не удалось получить данные из реестра: %w", err)
	}

	// Преобразует в шестнадцатеричный формат (8 символов, нижний регистр)
	return fmt.Sprintf("%08x", baseTime), nil
}
