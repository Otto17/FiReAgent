// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

// GetOutboundIP получает исходящий IP-адрес используемый для выхода в интернет
func GetOutboundIP() (net.IP, error) {
	conn, err := net.Dial("udp", "77.88.8.8:443")
	if err != nil {
		return nil, fmt.Errorf("не удалось подключиться для определения IP: %v", err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	// log.Printf("Глобальный IP-адрес: '%s'", localAddr.IP)
	return localAddr.IP, nil
}

// sendLocalIP отправляет локальный IP-адрес через MQTT-брокер
func sendLocalIP(cm *autopaho.ConnectionManager) {
	type IPMessage struct {
		LocalIP string `json:"LocalIP"`
	}
	var ipMsg IPMessage

	// Получает IP-адрес для включения его в сообщение
	if ip, err := GetOutboundIP(); err != nil {
		ipMsg.LocalIP = "Не определён!"
	} else {
		ipMsg.LocalIP = ip.String()
	}

	// Сериализует структуру сообщения для передачи по сети
	msg, err := json.Marshal(ipMsg)
	if err != nil {
		log.Printf("Ошибка сериализации локального IP-адреса: %v", err)
		return
	}

	// Отправляет сообщение с гарантированным качеством обслуживания
	if _, err := cm.Publish(context.Background(), &paho.Publish{
		QoS:     2,
		Topic:   "Data/DB",
		Payload: msg,
	}); err != nil {
		log.Printf("Ошибка отправки IP-адреса: %v", err)
	} else {
		// log.Printf("Локальный IP-адрес '%s' успешно отправлен", ipMsg.LocalIP)
	}
}
