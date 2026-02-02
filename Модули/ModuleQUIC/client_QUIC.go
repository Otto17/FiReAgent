// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/zeebo/xxh3"
)

const (
	statusErr byte = 1 // Статус ошибки протокола QUIC со стороны сервера

	// Коды ошибок протокола QUIC со стороны сервера
	ErrInvalidToken    uint16 = 1 // Неверный или просроченный токен
	ErrSessionNotFound uint16 = 2 // Не найдена сессия по токену
	ErrEmptyFileName   uint16 = 3 // В сессии не указано имя файла
	ErrFileOpen        uint16 = 4 // Файл отсутствует или недоступен на сервере
	ErrFileStat        uint16 = 5 // Ошибка получения информации о файле
	ErrBadOffset       uint16 = 6 // Смещение превышает размер файла

	maxDownloadAttempts    = 3               // Кол-во попыток перекачивания файла
	retryDelayBetweenTries = 2 * time.Second // Задержка между повторными попытками
)

// ServerError представляет ошибку, возвращаемую сервером клиенту (код + текстовое описание)
type ServerError struct {
	Code uint16 // Содержит код ошибки
	Msg  string // Содержит описание ошибки
}

// Error реализует интерфейс ошибки для ServerError
func (e ServerError) Error() string {
	return fmt.Sprintf("Ошибка со стороны сервера (%d): %s", e.Code, e.Msg)
}

// DownloadFile скачивает файл с сервера по протоколу QUIC с поддержкой докачки
func DownloadFile(token, expectedXXH3, mqttID string, downloadPath string, quicURL, portQUIC string, serverCaCert, clientCert, clientKey []byte) string {
	log.Printf("Начало скачивания в \"ModuleQUIC\" с токеном: %s, mqttID: %s", token, mqttID)

	// Настройка TLS с использованием полученных сертификатов
	serverCAPool := x509.NewCertPool()
	if !serverCAPool.AppendCertsFromPEM(serverCaCert) {
		clearSensitive(serverCaCert, clientCert, clientKey) // Обнуляем сертификаты в ОЗУ
		return createResponse("Ошибка", "", "не удалось добавить CA сертификат")
	}

	certificate, err := tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		clearSensitive(serverCaCert, clientCert, clientKey) // Обнуляем сертификаты в ОЗУ
		return createResponse("Ошибка", "", fmt.Sprintf("не удалось загрузить пару ключей: %v", err))
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{certificate},
		InsecureSkipVerify: false, // Включение проверки подлинности сертификата сервера
		RootCAs:            serverCAPool,
		NextProtos:         []string{"quic-file-transfer"},
	}

	var lastComputedHash string
	attemptResult := "Ошибка"
	attempts := 0

	// Подготовка адреса для подключения
	host := strings.TrimSpace(quicURL)
	if host == "" {
		clearSensitive(serverCaCert, clientCert, clientKey)
		return createResponse("Ошибка", "", "пустой URL для подключения к QUIC-серверу")
	}

	// Получаем порт для QUIC-сервера
	port := strings.TrimSpace(portQUIC)
	if port == "" {
		clearSensitive(serverCaCert, clientCert, clientKey)
		return createResponse("Ошибка", "", "пустой порт для подключения к QUIC-серверу")
	}
	addr := net.JoinHostPort(host, port) // Формируем адрес и порт одной строкой

	for attempt := 0; attempt < maxDownloadAttempts; attempt++ {
		lastComputedHash = "" // Сброс перед новой попыткой
		attempts = attempt + 1
		resumeFrom := uint64(0)
		var fileSize uint64

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		conn, err := quic.DialAddr(ctx, addr, tlsConfig, &quic.Config{})
		if err != nil {
			WriteToLogFile("Попытка %d: ошибка подключения к QUIC серверу: %v", attempt+1, err)
			time.Sleep(retryDelayBetweenTries) // Ждём перед следующей попыткой
			continue
		}

		stream, err := conn.OpenStreamSync(ctx)
		if err != nil {
			WriteToLogFile("Попытка %d: ошибка открытия потока: %v", attempt+1, err)
			conn.CloseWithError(0, "")
			time.Sleep(retryDelayBetweenTries) // Ждём перед следующей попыткой
			continue
		}

		// Отправка токена
		if err := sendData(stream, []byte(token)); err != nil {
			WriteToLogFile("Попытка %d: ошибка отправки токена: %v", attempt+1, err)
			stream.Close()
			conn.CloseWithError(0, "")
			continue
		}

		// Отправка mqttID
		if err := sendData(stream, []byte(mqttID)); err != nil {
			WriteToLogFile("Попытка %d: ошибка отправки MQTT ID: %v", attempt+1, err)
			stream.Close()
			conn.CloseWithError(0, "")
			continue
		}

		// Отправка смещения
		if err := binary.Write(stream, binary.BigEndian, resumeFrom); err != nil {
			WriteToLogFile("Попытка %d: ошибка отправки смещения: %v", attempt+1, err)
			stream.Close()
			conn.CloseWithError(0, "")
			continue
		}

		// Получение метаданных файла
		_, fileSize, err = receiveMetadata(stream)
		if err != nil {
			WriteToLogFile("Попытка %d: ошибка получения метаданных: %v", attempt+1, err)

			// Если сервер прислал осмысленную ошибку — не повторяем попытку загрузки
			var sErr ServerError
			if errors.As(err, &sErr) {
				stream.Close()
				conn.CloseWithError(0, "")
				clearSensitive(serverCaCert, clientCert, clientKey, &certificate)

				return createResponse("Ошибка", fmt.Sprintf("%d", attempts), sErr.Msg)
			}

			// Иначе это сеть/временный сбой — повторяем попытку
			stream.Close()
			conn.CloseWithError(0, "")
			time.Sleep(retryDelayBetweenTries)
			continue
		}

		// Создание файла по указанному пути
		file, err := os.Create(downloadPath)
		if err != nil {
			clearSensitive(serverCaCert, clientCert, clientKey, &certificate)
			stream.Close()
			conn.CloseWithError(0, "")
			return createResponse("Ошибка", fmt.Sprintf("%d", attempts), fmt.Sprintf("ошибка создания файла: %v", err))
		}

		// Скачивание файла
		if success := downloadStream(stream, file, fileSize, expectedXXH3, &lastComputedHash, serverCaCert, clientCert, clientKey, &certificate); success {
			attemptResult = "Успех"
			break
		}

		file.Close()
		stream.Close()
		conn.CloseWithError(0, "")
		os.Remove(downloadPath)

		// Ждём перед следующей попыткой
		time.Sleep(retryDelayBetweenTries)
	}

	if attemptResult == "Успех" {
		return createResponse("Успех", fmt.Sprintf("%d", attempts), "")
	}

	// После всех попыток
	errorDesc := "Не удалось скачать файл с трёх попыток"
	if lastComputedHash != "" {
		errorDesc = fmt.Sprintf("Хеш-суммы не совпадают. Вычисленный хеш: \"%s\", ожидаемый хеш: \"%s\"", lastComputedHash, expectedXXH3)
	}

	return createResponse("Ошибка", fmt.Sprintf("%d", attempts), errorDesc)
}

// sendData отправляет данные через QUIC stream с указанием длины
func sendData(stream *quic.Stream, data []byte) error {
	if err := binary.Write(stream, binary.BigEndian, uint16(len(data))); err != nil {
		return err
	}
	_, err := stream.Write(data)
	return err
}

// receiveMetadata получает метаданные файла из QUIC stream
func receiveMetadata(stream *quic.Stream) (string, uint64, error) {
	// Читаем статус QUIC протокола
	var status byte
	if err := binary.Read(stream, binary.BigEndian, &status); err != nil {
		return "", 0, err
	}

	if status == statusErr {
		var code uint16
		if err := binary.Read(stream, binary.BigEndian, &code); err != nil {
			return "", 0, err
		}
		var msgLen uint16
		if err := binary.Read(stream, binary.BigEndian, &msgLen); err != nil {
			return "", 0, err
		}
		msg := make([]byte, msgLen)
		if _, err := io.ReadFull(stream, msg); err != nil {
			return "", 0, err
		}
		return "", 0, ServerError{Code: code, Msg: string(msg)}
	}

	// Если статус OK — читаем метаданные
	var fileNameLen uint16
	if err := binary.Read(stream, binary.BigEndian, &fileNameLen); err != nil {
		return "", 0, err
	}

	fileNameBytes := make([]byte, fileNameLen)
	if _, err := io.ReadFull(stream, fileNameBytes); err != nil {
		return "", 0, err
	}

	var fileSize uint64
	if err := binary.Read(stream, binary.BigEndian, &fileSize); err != nil {
		return "", 0, err
	}

	return string(fileNameBytes), fileSize, nil
}

// downloadStream скачивает данные из QUIC stream в файл и проверяет хеш
func downloadStream(stream *quic.Stream, file *os.File, fileSize uint64, expectedXXH3 string, lastComputedHash *string, serverCaCert, clientCert, clientKey []byte, certificate *tls.Certificate) bool {
	buf := make([]byte, getBufferSize(fileSize, 0))
	var received uint64

	// Потоковый хешер методом "XXH3"
	hasher := xxh3.New()

	for {
		n, err := stream.Read(buf)
		if n > 0 {
			// Пишем на диск
			if _, wErr := file.Write(buf[:n]); wErr != nil {
				WriteToLogFile("Ошибка записи в файл: %v", wErr)
				clearSensitive(serverCaCert, clientCert, clientKey, certificate)
				return false
			}
			// Одновременно обновляем хеш
			if _, hErr := hasher.Write(buf[:n]); hErr != nil {
				WriteToLogFile("Ошибка обновления XXH3: %v", hErr)
				clearSensitive(serverCaCert, clientCert, clientKey, certificate)
				return false
			}
			received += uint64(n)
		}

		if err != nil {
			if err == io.EOF || received >= fileSize {
				// На всякий случай — синхронизируем запись перед финальной проверкой
				if fsyncErr := file.Sync(); fsyncErr != nil {
					WriteToLogFile("Ошибка Sync файла перед проверкой хеша: %v", fsyncErr)
				}
				file.Close()

				// Финальный хеш "на лету"
				computedHash := fmt.Sprintf("%016x", hasher.Sum64())
				WriteToLogFile("Вычисленный XXH3 (на лету): %s, Ожидаемый: %s", computedHash, expectedXXH3)

				if computedHash == expectedXXH3 {
					clearSensitive(serverCaCert, clientCert, clientKey, certificate)
					return true
				}

				*lastComputedHash = computedHash
				return false
			}
			// Иная ошибка чтения
			WriteToLogFile("Ошибка чтения из QUIC потока: %v", err)
			clearSensitive(serverCaCert, clientCert, clientKey, certificate)
			return false
		}
	}
}

// getBufferSize определяет размер буфера в зависимости от оставшегося размера файла
func getBufferSize(fileSize, resumeFrom uint64) int {
	remaining := fileSize - resumeFrom

	if remaining < 1<<20 { // меньше 1 МБ
		return 16 << 10 // 16 КБ
	}
	if remaining > 100<<20 { // больше 100 МБ
		return 256 << 10 // 256 КБ
	}
	return 64 << 10 // по умолчанию 64 КБ
}
