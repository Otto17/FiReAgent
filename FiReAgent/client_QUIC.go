// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
)

// QUICData описывает структуру данных, передаваемых модулю ModuleQUIC.exe
type QUICData struct {
	OnlyDownload                  bool   `json:"OnlyDownload"`
	DownloadRunPath               string `json:"DownloadRunPath"`
	ProgramRunArguments           string `json:"ProgramRunArguments"`
	RunWhetherUserIsLoggedOnOrNot bool   `json:"RunWhetherUserIsLoggedOnOrNot"`
	UserName                      string `json:"UserName"`
	UserPassword                  string `json:"UserPassword"`
	RunWithHighestPrivileges      bool   `json:"RunWithHighestPrivileges"`
	NotDeleteAfterInstallation    bool   `json:"NotDeleteAfterInstallation"`
	XXH3                          string `json:"XXH3"`
	Token                         string `json:"Token"`
	MqttID                        string `json:"mqttID"`
	URL                           string `json:"URL"`
	PortQUIC                      string `json:"PortQUIC"`
	ServerCaCert                  []byte `json:"serverCaCert"`
	ClientCert                    []byte `json:"clientCert"`
	ClientKey                     []byte `json:"clientKey"`
}

// processQUICMessage обрабатывает входящее MQTT-сообщение для выполнения QUIC-задач
func processQUICMessage(mqttSvc *MQTTService, message []byte) error {
	// Объявляет структуру для парсинга входящего сообщения, включая `Date_Of_Creation`
	var data struct {
		DateOfCreation                string `json:"Date_Of_Creation"`
		OnlyDownload                  bool   `json:"OnlyDownload"`
		DownloadRunPath               string `json:"DownloadRunPath"`
		ProgramRunArguments           string `json:"ProgramRunArguments"`
		RunWhetherUserIsLoggedOnOrNot bool   `json:"RunWhetherUserIsLoggedOnOrNot"`
		UserName                      string `json:"UserName"`
		UserPassword                  string `json:"UserPassword"`
		RunWithHighestPrivileges      bool   `json:"RunWithHighestPrivileges"`
		NotDeleteAfterInstallation    bool   `json:"NotDeleteAfterInstallation"`
		XXH3                          string `json:"XXH3"`
		Token                         string `json:"Token"`
	}

	if err := json.Unmarshal(message, &data); err != nil {
		return fmt.Errorf("ошибка разбора JSON: %v", err)
	}

	// log.Printf("Получен токен из сообщения: %s", data.Token) // ДЛЯ ОТЛАДКИ

	// Получает параметры QUIC-подключения и mTLS-сертификаты из криптомодуля
	urlQUIC, portQUIC, serverCaCert, clientCert, clientKey, err := getQUICFromCrypto()
	if err != nil {
		return fmt.Errorf("ошибка получения данных подключения и сертификатов: %v", err)
	}

	// Формирует полный набор данных для передачи во внешний модуль
	quicData := QUICData{
		OnlyDownload:                  data.OnlyDownload,
		DownloadRunPath:               data.DownloadRunPath,
		ProgramRunArguments:           data.ProgramRunArguments,
		RunWhetherUserIsLoggedOnOrNot: data.RunWhetherUserIsLoggedOnOrNot,
		UserName:                      data.UserName,
		UserPassword:                  data.UserPassword,
		RunWithHighestPrivileges:      data.RunWithHighestPrivileges,
		NotDeleteAfterInstallation:    data.NotDeleteAfterInstallation,
		XXH3:                          data.XXH3,
		Token:                         data.Token,
		MqttID:                        mqttSvc.mqttID,
		URL:                           urlQUIC,
		PortQUIC:                      portQUIC,
		ServerCaCert:                  serverCaCert,
		ClientCert:                    clientCert,
		ClientKey:                     clientKey,
	}

	dataBytes, err := json.Marshal(quicData)
	if err != nil {
		return fmt.Errorf("ошибка сериализации JSON: %v", err)
	}

	// Генерирует уникальный ID для создания Named Pipe
	pipeGUID := uuid.New().String()

	// Получает BaseTimeHex для защиты от несанкционированного запуска модуля
	baseTimeHex, err := GetBaseTimeHex()
	if err != nil {
		return fmt.Errorf("ошибка получения BaseTimeHex: %v", err)
	}

	// log.Printf("Отправляем токен в ModuleQUIC: %s", quicData.Token) // ДЛЯ ОТЛАДКИ

	// Запускает модуль "ModuleQUIC.exe" и устанавливает соединение через именной канал
	conn, err := StartModuleAndConnect("ModuleQUIC.exe", pipeGUID, baseTimeHex)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Отправляет сериализованные данные QUIC-задачи во внешний модуль
	if err := SendPipeData(conn, dataBytes); err != nil {
		return fmt.Errorf("ошибка отправки данных в канал: %v", err)
	}

	// Очищает конфиденциальные данные сертификатов после передачи, чтобы уменьшить риск
	defer clearSensitive(serverCaCert, clientCert, clientKey)
	defer func() {
		// Очищает пароль пользователя, если он был предоставлен
		if data.UserPassword != "" {
			passBytes := []byte(data.UserPassword)
			for i := range passBytes {
				passBytes[i] = 0
			}
		}
	}()

	// Читает бинарный ответ от ModuleQUIC.exe
	responseBytes, err := ReadPipeData(conn)
	if err != nil {
		return fmt.Errorf("ошибка чтения результата: %v", err)
	}

	// Десериализует ответ модуля для извлечения результата выполнения
	var moduleResp struct {
		QUIC_Execution string `json:"QUIC_Execution"`
		Attempts       string `json:"Attempts"`
		Description    string `json:"Description"`
		Answer         string `json:"Answer"`
	}
	if err := json.Unmarshal(responseBytes, &moduleResp); err != nil {
		return fmt.Errorf("ошибка разбора ответа модуля: %v", err)
	}

	// Формирует итоговый ответ, включая оригинальный DateOfCreation
	answerMsg := struct {
		DateOfCreation string `json:"Date_Of_Creation"`
		QUIC_Execution string `json:"QUIC_Execution"`
		Attempts       string `json:"Attempts"`
		Description    string `json:"Description"`
		Answer         string `json:"Answer"`
	}{
		DateOfCreation: data.DateOfCreation,
		QUIC_Execution: moduleResp.QUIC_Execution,
		Attempts:       moduleResp.Attempts,
		Description:    moduleResp.Description,
		Answer:         moduleResp.Answer,
	}

	answerJSON, err := json.Marshal(answerMsg)
	if err != nil {
		return fmt.Errorf("ошибка сериализации JSON-ответа: %v", err)
	}

	// Публикует ответ в MQTT-топик с гарантией доставки (QoS 2)
	topic := fmt.Sprintf("Client/%s/ModuleQUIC/Answer", mqttSvc.mqttID)
	if _, err := mqttSvc.client.Publish(context.Background(), &paho.Publish{
		Topic:   topic,
		Payload: answerJSON,
		QoS:     2,
	}); err != nil {
		return fmt.Errorf("ошибка отправки ответа: %v", err)
	}

	// log.Printf("Ответ отправлен в топик %s: %s", topic, string(answerJSON))
	return nil
}

// getQUICFromCrypto запрашивает mTLS сертификаты и параметры подключения QUIC у криптомодуля
func getQUICFromCrypto() (string, string, []byte, []byte, []byte, error) {
	pipeGUID := uuid.New().String()
	baseTimeHex, err := GetBaseTimeHex()
	if err != nil {
		return "", "", nil, nil, nil, fmt.Errorf("ошибка получения BaseTimeHex: %v", err)
	}

	// Запускает "ModuleCrypto.exe" с аргументом "half" для получения ограниченного набора данных
	conn, err := StartModuleAndConnect("ModuleCrypto.exe", pipeGUID, baseTimeHex, "half")
	if err != nil {
		return "", "", nil, nil, nil, err
	}
	defer conn.Close()

	// Читает статус от ModuleCrypto (первое сообщение)
	status, err := ReadPipeData(conn)
	if err != nil {
		return "", "", nil, nil, nil, fmt.Errorf("ошибка чтения статуса от ModuleCrypto: %v", err)
	}

	// Проверяет статус перед чтением данных
	switch string(status) {
	case "OK":
		// Продолжает чтение данных
	case "CERT_NOT_FOUND":
		return "", "", nil, nil, nil, fmt.Errorf("сертификат 'CryptoAgent' не найден")
	case "CERTS_MISSING":
		return "", "", nil, nil, nil, fmt.Errorf("зашифрованные файлы сертификатов отсутствуют")
	case "DECRYPT_ERROR":
		return "", "", nil, nil, nil, fmt.Errorf("ошибка расшифровки данных")
	case "CONFIG_ERROR":
		return "", "", nil, nil, nil, fmt.Errorf("ошибка конфигурации")
	default:
		return "", "", nil, nil, nil, fmt.Errorf("ошибка от ModuleCrypto: %s", status)
	}

	// Читает последовательные бинарные блоки данных из Named Pipe
	urlQUICBytes, err := ReadPipeData(conn)
	if err != nil {
		return "", "", nil, nil, nil, fmt.Errorf("ошибка чтения URL: %v", err)
	}

	portQUICBytes, err := ReadPipeData(conn)
	if err != nil {
		return "", "", nil, nil, nil, fmt.Errorf("ошибка чтения PortQUIC: %v", err)
	}

	serverCaCert, err := ReadPipeData(conn)
	if err != nil {
		return "", "", nil, nil, nil, fmt.Errorf("ошибка чтения CA-сертификата: %v", err)
	}

	clientCert, err := ReadPipeData(conn)
	if err != nil {
		return "", "", nil, nil, nil, fmt.Errorf("ошибка чтения клиентского сертификата: %v", err)
	}

	clientKey, err := ReadPipeData(conn)
	if err != nil {
		return "", "", nil, nil, nil, fmt.Errorf("ошибка чтения клиентского ключа: %v", err)
	}

	return string(urlQUICBytes), string(portQUICBytes), serverCaCert, clientCert, clientKey, nil
}
