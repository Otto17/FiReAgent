// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
)

// ReceivedCommandMessage описывает структуру входящего MQTT-сообщения с командой
type ReceivedCommandMessage struct {
	DateOfCreation                string `json:"Date_Of_Creation"`
	Terminal                      string `json:"Terminal"`
	Command                       string `json:"Command"`
	WorkingFolder                 string `json:"WorkingFolder"`
	RunWhetherUserIsLoggedOnOrNot bool   `json:"RunWhetherUserIsLoggedOnOrNot"`
	User                          string `json:"User"`
	Password                      string `json:"Password"`
	RunWithHighestPrivileges      bool   `json:"RunWithHighestPrivileges"`
	CaptureOutput                 *bool  `json:"CaptureOutput,omitempty"`
	OutputMaxBytes                *int   `json:"OutputMaxBytes,omitempty"`
	OutputFolder                  string `json:"OutputFolder,omitempty"`
}

// CommandMessage описывает структуру данных для передачи во внешний модуль
type CommandMessage struct {
	Terminal                      string `json:"Terminal"`
	Command                       string `json:"Command"`
	WorkingFolder                 string `json:"WorkingFolder"`
	RunWhetherUserIsLoggedOnOrNot bool   `json:"RunWhetherUserIsLoggedOnOrNot"`
	User                          string `json:"User"`
	Password                      string `json:"Password"`
	RunWithHighestPrivileges      bool   `json:"RunWithHighestPrivileges"`
	CaptureOutput                 bool   `json:"CaptureOutput"`
	OutputMaxBytes                int    `json:"OutputMaxBytes"`
	OutputFolder                  string `json:"OutputFolder,omitempty"`
}

// processMCMessage обрабатывает входящее сообщение, запускает модуль и публикует ответ
func processMCMessage(mqttSvc *MQTTService, message []byte) error {
	// Распарсивает входящий JSON для доступа ко всем полям, включая `Date_Of_Creation`
	var received ReceivedCommandMessage
	if err := json.Unmarshal(message, &received); err != nil {
		return fmt.Errorf("ошибка разбора входящего JSON: %v", err)
	}

	// Устанавливает значения по умолчанию, если поля не были указаны в JSON
	co := true
	if received.CaptureOutput != nil {
		co = *received.CaptureOutput
	}
	omb := 262144 // Устанавливает 256 КБ по умолчанию для ограничения размера вывода
	if received.OutputMaxBytes != nil && *received.OutputMaxBytes > 0 {
		omb = *received.OutputMaxBytes
	}

	// Временно сохраняет конфиденциальные данные, прежде чем обнулить их в исходной структуре
	userBytes := []byte(received.User)
	passwordBytes := []byte(received.Password)
	received.User = ""
	received.Password = ""

	// Формирует структуру для передачи данных в модуль
	cmdMsg := CommandMessage{
		Terminal:                      received.Terminal,
		Command:                       received.Command,
		WorkingFolder:                 received.WorkingFolder,
		RunWhetherUserIsLoggedOnOrNot: received.RunWhetherUserIsLoggedOnOrNot,
		User:                          string(userBytes),
		Password:                      string(passwordBytes),
		RunWithHighestPrivileges:      received.RunWithHighestPrivileges,
		CaptureOutput:                 co,
		OutputMaxBytes:                omb,
		OutputFolder:                  received.OutputFolder,
	}
	cmdData, err := json.Marshal(cmdMsg)
	if err != nil {
		return fmt.Errorf("ошибка сериализации JSON для модуля: %v", err)
	}

	// Генерирует уникальный ID для создания Named Pipe, чтобы обеспечить изолированность вызова
	pipeGUID := uuid.New().String()

	// Получает BaseTimeHex для проверки подлинности и защиты запуска модуля
	baseTimeHex, err := GetBaseTimeHex()
	if err != nil {
		return fmt.Errorf("ошибка получения BaseTimeHex: %v", err)
	}

	// Запускает внешний модуль и устанавливает соединение через именнованный канал
	conn, err := StartModuleAndConnect("ModuleCommand.exe", pipeGUID, baseTimeHex)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Отправляет сериализованные данные команды во внешний модуль
	if err := SendPipeData(conn, cmdData); err != nil {
		return fmt.Errorf("ошибка отправки данных в канал: %v", err)
	}

	// Читает полный ответ (включая stderr/stdout) от внешнего модуля
	responseBytes, err := ReadPipeData(conn)
	if err != nil {
		return fmt.Errorf("ошибка чтения ответа из канала: %v", err)
	}
	response := string(responseBytes)
	// log.Printf("Получен ответ от модуля ModuleCommand.exe: %s", response)

	// Очищает байтовые срезы, содержащие пароль и логин, чтобы минимизировать риски утечки
	defer clearSensitive(userBytes, passwordBytes)

	// Форматирует время завершения выполнения команды
	finishTime := time.Now().Format("02.01.06(15:04:05)")

	// Пытается десериализовать ответ модуля в формате JSON
	var moduleResp map[string]any
	if err := json.Unmarshal(responseBytes, &moduleResp); err != nil {
		// Если ответ не является JSON, он сохраняется как сырая строка
		moduleResp = map[string]any{
			"Raw": response,
		}
	}

	// Формирует окончательный ответ, включая исходный `Date_Of_Creation`
	answerMsg := map[string]any{
		"Date_Of_Creation": received.DateOfCreation, // Сохраняет оригинальный идентификатор запроса
		"Answer":           finishTime,
		"ModuleResult":     moduleResp, // Включает структурированный или сырой вывод модуля
	}

	answerJSON, err := json.Marshal(answerMsg)
	if err != nil {
		return fmt.Errorf("ошибка сериализации JSON-ответа: %v", err)
	}

	// Публикует ответ обратно на сервер с гарантией доставки (QoS 2)
	topic := fmt.Sprintf("Client/%s/ModuleCommand/Answer", mqttSvc.mqttID)
	if _, err := mqttSvc.client.Publish(context.Background(), &paho.Publish{
		Topic:   topic,
		Payload: answerJSON,
		QoS:     2,
	}); err != nil {
		return fmt.Errorf("ошибка отправки ответа: %v", err)
	}

	// log.Printf("Ответ отправлен в топик %s: %s", topic, answerJSON)
	return nil
}
