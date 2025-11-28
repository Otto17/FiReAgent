// Copyright (c) 2025 Otto
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
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// missingCertLogOnce гарантирует одноразовый запуск ModuleCrypto
var missingCertLogOnce sync.Once

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
	maxWait := 3 * time.Second
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

// isCryptoAgentCertInstalled проверяет, установлен ли сертификат "CryptoAgent" в хранилище "Локальный компьютер\\Личное"
func isCryptoAgentCertInstalled() (bool, error) {
	// Использует библиотеку Crypt32 для работы с хранилищем сертификатов
	crypt32 := windows.NewLazySystemDLL("crypt32.dll")
	procCertOpenStore := crypt32.NewProc("CertOpenStore")                           // Получает адрес функции открытия хранилища
	procCertFindCertificateInStore := crypt32.NewProc("CertFindCertificateInStore") // Получает адрес функции поиска сертификата
	procCertFreeCertificateContext := crypt32.NewProc("CertFreeCertificateContext") // Получает адрес функции освобождения контекста
	procCertCloseStore := crypt32.NewProc("CertCloseStore")                         // Получает адрес функции закрытия хранилища

	// Флаги Windows API необходимые для работы с функциями Crypt32
	const (
		CERT_STORE_PROV_SYSTEM_W        = 0x0000000A // Провайдер системного хранилища
		CERT_SYSTEM_STORE_LOCAL_MACHINE = 0x00020000 // Хранилище Локального компьютера
		CERT_STORE_READONLY_FLAG        = 0x00008000 // Режим только для чтения

		X509_ASN_ENCODING   = 0x00000001 // Кодировка X509
		PKCS_7_ASN_ENCODING = 0x00010000 // Кодировка PKCS7

		CERT_FIND_SUBJECT_STR_W = 0x00080007 // Метод поиска по строке Subject
	)

	// Открывает хранилище LocalMachine\My для поиска сертификата
	storeName, _ := windows.UTF16PtrFromString("My")
	hStore, _, err := procCertOpenStore.Call(
		uintptr(CERT_STORE_PROV_SYSTEM_W),
		0,
		0,
		uintptr(CERT_SYSTEM_STORE_LOCAL_MACHINE|CERT_STORE_READONLY_FLAG),
		uintptr(unsafe.Pointer(storeName)),
	)

	if hStore == 0 {
		return false, fmt.Errorf("CertOpenStore(LocalMachine\\My) failed: %v", err)
	}
	defer procCertCloseStore.Call(hStore, 0)

	// Ищет сертификат по строке Subject "CryptoAgent"
	subj, _ := windows.UTF16PtrFromString("CryptoAgent")
	ctx, _, _ := procCertFindCertificateInStore.Call(
		hStore,
		uintptr(X509_ASN_ENCODING|PKCS_7_ASN_ENCODING),
		0,
		uintptr(CERT_FIND_SUBJECT_STR_W),
		uintptr(unsafe.Pointer(subj)),
		0,
	)
	if ctx != 0 {
		procCertFreeCertificateContext.Call(ctx)
		return true, nil
	}
	return false, nil
}

// logMissingCertOnce выполняет однократное логирование отсутствия необходимого сертификата
func logMissingCertOnce() {
	missingCertLogOnce.Do(func() { // Использует "Do" для гарантии однократного выполнения логирования
		baseTimeHex, err := GetBaseTimeHex()
		if err != nil {
			return
		}

		exePath, err := os.Executable()
		if err != nil {
			return
		}
		modulePath := filepath.Join(filepath.Dir(exePath), "ModuleCrypto.exe")

		// Запускает модуль без pipe-режима чтобы обеспечить немедленное логирование ошибки сертификата
		cmd := exec.Command(modulePath, baseTimeHex, "full")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

		// Ожидание завершения гарантирует запись лога перед возможной остановкой службы
		_ = cmd.Run() // Выполняет запуск поскольку ошибки здесь не являются критичными
	})
}
