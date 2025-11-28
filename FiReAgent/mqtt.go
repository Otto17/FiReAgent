// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
)

// authIncompleteLogOnce гарантирует однократное выполнение ModuleCrypto
var authIncompleteLogOnce sync.Once

// MQTTService инкапсулирует данные MQTT-клиента
type MQTTService struct {
	client      *autopaho.ConnectionManager
	mqttID      string
	liteSender  *ReportSender // Ссылка на отправитель Lite
	aidaSender  *ReportSender // Ссылка на отправитель Aida
	reportLock  sync.Mutex    // Мьютекс для безопасного доступа к отправителям
	isConnected bool          // Текущее состояние подключения
	connLock    sync.RWMutex  // Мьютекс для состояния подключения
	ops         *OpTracker    // Трекер операций, отслеживает активные задачи и управляет их завершением
	connectedAt time.Time     // Хранит время запуска сервиса для определения приоритета при конфликте ID клиентов
}

// StartMQTTClient создаёт MQTT-соединение и возвращает объект MQTTService
func StartMQTTClient() *MQTTService {
	ctx := context.Background()
	tlsConfig, urlBroker, portMQTT, loginMQTT, passwordMQTT, mqttID, err := createTLSConfig()
	if err != nil {
		log.Fatalf("Ошибка при создании TLS-конфигурации: %v", err)
	}

	// Инициализирует объект сервиса, трекер и сохраняет mqttID
	svc := &MQTTService{
		mqttID:      mqttID,
		ops:         NewOpTracker(), // Инициализация трекера
		connectedAt: time.Now(),     // Фиксирует время старта сервиса (не обновляется при разрывах сети)
	}

	// Использует TLS-соединение при парсинге URL брокера
	brokerURL, err := url.Parse(fmt.Sprintf("tls://%s:%s", urlBroker, portMQTT))
	if err != nil {
		log.Fatalf("Ошибка парсинга URL: %v", err)
	}

	cliCfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     20,                   // Интервал KeepAlive в секундах
		CleanStartOnInitialConnection: true,                 // Запрос чистой сессии
		SessionExpiryInterval:         0,                    // Сессия завершается при разрыве соединения
		ConnectUsername:               loginMQTT,            // Логин
		ConnectPassword:               []byte(passwordMQTT), // Пароль

		ClientConfig: paho.ClientConfig{
			ClientID: mqttID, // ID клиента

			OnClientError: func(err error) {
				log.Printf("Клиентская ошибка: %v", err)
			},

			// Обработка входящих сообщений, замыкание захватывает svc
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					// Не запускать новые задачи, если идёт остановка FiReAgent
					if svc.ops.IsStopping() {
						// log.Printf("Получено сообщение %s, но агент в процессе остановки — пропускаем", pr.Packet.Topic)
						return true, nil
					}

					// Безопасно копирует данные до запуска горутины
					topic := pr.Packet.Topic
					payload := append([]byte(nil), pr.Packet.Payload...) // Глубокая копия

					// Запускает обработки как "операции" с учётом трекера
					run := func(name string, fn func() error) {
						done, ok := svc.ops.Start()
						if !ok {
							// log.Printf("Задача %s не запущена: агент в процессе остановки", name)
							return
						}

						// Обработка сообщений запускается в отдельной горутине, чтобы не блокировать поток MQTT
						go func() {
							defer done()
							if err := fn(); err != nil {
								log.Printf("Ошибка в задаче %s: %v", name, err)
							}
						}()
					}

					switch topic {
					case fmt.Sprintf("Client/%s/ModuleCommand", svc.mqttID):
						// Обрабатывает команды cmd и PowerShell
						run("ModuleCommand", func() error { return processMCMessage(svc, payload) })
					case fmt.Sprintf("Client/%s/ModuleQUIC", svc.mqttID):
						// Обрабатывает QUIC загрузки и установки
						run("ModuleQUIC", func() error { return processQUICMessage(svc, payload) })
					case fmt.Sprintf("Client/%s/Uninstaller", svc.mqttID):
						// Обрабатывает команду самоудаления агента
						run("Uninstaller", func() error { return processUninstallMessage(svc, payload) })
					}
					return true, nil
				},
			},

			OnServerDisconnect: func(d *paho.Disconnect) {
				// Устанавливает флаг отключения
				svc.setConnected(false)

				// Обрабатывает конфликт "Session Taken Over", код 142 (0x8E), возникающий при дублировании ID
				if d.ReasonCode == 142 {
					// Вычисляет длительность работы сервиса с момента запуска
					sessionDuration := time.Since(svc.connectedAt)

					// Определяет временной порог для признания клиента дубликатом (копией)
					const newbieThreshold = 10 * time.Second

					log.Printf("СЕРВЕР: Принудительное отключение (Session takeover). Время работы агента: %v", sessionDuration)

					if sessionDuration < newbieThreshold {
						log.Printf("ОБНАРУЖЕН КОНФЛИКТ: Время работы %v (меньше порога %v). Сброс ID и перезапуск.", sessionDuration, newbieThreshold)

						// Удаляет файл конфигурации ID
						if err := deleteMqttIDConfig(); err != nil {
							log.Printf("Ошибка удаления MqttID.conf: %v", err)
						} else {
							log.Println("Файл MqttID.conf удален.")
						}

						// Завершает процесс для перезапуска службы и генерации нового ID
						os.Exit(1)
					} else {
						log.Printf("ОБНАРУЖЕН КОНФЛИКТ: Время работы %v (признак оригинала). Ожидание автоматического переподключения...", sessionDuration)
						// Игнорирует ошибку, позволяя Autopaho выполнить переподключение и вытеснить дубликат
					}
				}

				if d.Properties != nil {
					log.Printf("Сервер запросил отключение: %s", d.Properties.ReasonString)
				} else {
					log.Printf("Сервер запросил отключение; код причины: %d", d.ReasonCode)
				}
			},
		},

		OnConnectionUp: func(cm *autopaho.ConnectionManager, connAck *paho.Connack) {
			log.Println("Подключен к брокеру MQTT")

			// Устанавливает флаг подключения
			svc.setConnected(true)

			// Выполняет подписку на топики, специфичные для этого mqttID
			subscriptions := []paho.SubscribeOptions{
				{Topic: fmt.Sprintf("Client/%s/ModuleCommand", svc.mqttID), QoS: 2}, // Модуль для работы с cmd и PowerShell
				{Topic: fmt.Sprintf("Client/%s/ModuleQUIC", svc.mqttID), QoS: 2},    // Модуль для работы с QUIC
				{Topic: fmt.Sprintf("Client/%s/Uninstaller", svc.mqttID), QoS: 2},   // Команда на самоудаление агента
			}
			if _, err := cm.Subscribe(context.Background(), &paho.Subscribe{Subscriptions: subscriptions}); err != nil {
				log.Printf("Ошибка подписки: %v", err)
			} else {
				// log.Println("Подписка выполнена на топики:", subscriptions)
			}

			// Отправляет локальный IP-адрес
			if done, ok := svc.ops.Start(); ok {
				go func() {
					defer done()
					sendLocalIP(cm)
				}()
			}

			// Уведомляет отправители о восстановлении соединения
			svc.reportLock.Lock()
			defer svc.reportLock.Unlock()
			if svc.liteSender != nil {
				svc.liteSender.OnReconnect()
			}
			if svc.aidaSender != nil {
				svc.aidaSender.OnReconnect()
			}
		},

		OnConnectError: func(err error) {
			// Устанавливает флаг отключения
			svc.setConnected(false)
			log.Printf("Ошибка подключения: %v", err)
		},
	}

	// Создаёт ConnectionManager, который поддерживает соединение до отмены контекста
	connMgr, err := autopaho.NewConnection(ctx, cliCfg)
	if err != nil {
		log.Printf("Начальное подключение не удалось: %v", err)
	}
	svc.client = connMgr

	return svc
}

// deleteMqttIDConfig удаляет файл "MqttID.conf" для сброса текущего ID клиента
func deleteMqttIDConfig() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	configPath := filepath.Join(filepath.Dir(exePath), "config", "MqttID.conf")

	// Проверяет, существует ли файл перед удалением
	if _, err := os.Stat(configPath); err == nil {
		return os.Remove(configPath)
	}
	return nil
}

// DrainActiveOperations ожидает завершения всех активных задач с указанным таймаутом
func (svc *MQTTService) DrainActiveOperations(timeout time.Duration) bool {
	return svc.ops.WaitWithTimeout(timeout)
}

// setConnected устанавливает состояние подключения
func (svc *MQTTService) setConnected(state bool) {
	svc.connLock.Lock()
	defer svc.connLock.Unlock()
	svc.isConnected = state
}

// IsConnected проверяет текущее состояние подключения
func (svc *MQTTService) IsConnected() bool {
	svc.connLock.RLock()
	defer svc.connLock.RUnlock()
	return svc.isConnected
}

// SetReportSenders устанавливает отправители отчетов
func (svc *MQTTService) SetReportSenders(lite, aida *ReportSender) {
	svc.reportLock.Lock()
	defer svc.reportLock.Unlock()
	svc.liteSender = lite
	svc.aidaSender = aida
}

// createTLSConfig запрашивает расшифрованные данные у ModuleCrypto.exe через именованный канал
func createTLSConfig() (*tls.Config, string, string, string, string, string, error) {
	// Генерирует уникальное имя канала на основе GUID
	pipeGUID := uuid.New().String()

	// Получает BaseTimeHex (в HEX) для защиты от ручного запуска модуля
	baseTimeHex, err := GetBaseTimeHex()
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("ошибка получения данных из реестра: %v", err)
	}

	// Подключается к модулю ModuleCrypto.exe через именованный канал с аргументом "full"
	conn, err := StartModuleAndConnect("ModuleCrypto.exe", pipeGUID, baseTimeHex, "full")
	if err != nil {
		return nil, "", "", "", "", "", err
	}
	defer conn.Close()

	// Читает данные из канала в бинарном формате
	// Порядок данных в режиме "full": [ServerURL, PortMQTT, LoginMQTT, PasswordMQTT, mqttID, CaCert, ClientCert, ClientKey]
	urlBroker, err := ReadPipeData(conn)
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("ошибка чтения URL: %v", err)
	}
	portMQTT, err := ReadPipeData(conn)
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("ошибка чтения порта: %v", err)
	}

	loginMQTT, err := ReadPipeData(conn)
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("ошибка чтения логина: %v", err)
	}
	passwordMQTT, err := ReadPipeData(conn)
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("ошибка чтения пароля: %v", err)
	}

	mqttID, err := ReadPipeData(conn)
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("ошибка чтения mqtt_id: %v", err)
	}

	ServerCaCert, err := ReadPipeData(conn)
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("ошибка чтения CA-сертификата: %v", err)
	}

	clientCert, err := ReadPipeData(conn)
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("ошибка чтения клиентского сертификата: %v", err)
	}

	clientKey, err := ReadPipeData(conn)
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("ошибка чтения клиентского ключа: %v", err)
	}

	// Проверяет наличие пустых полей
	if len(loginMQTT) == 0 || len(passwordMQTT) == 0 || len(ServerCaCert) == 0 || len(clientCert) == 0 || len(clientKey) == 0 {
		// fmt.Println(string(loginMQTT), string(loginMQTT), string(ServerCaCert), string(clientCert), string(clientKey))
		return nil, "", "", "", "", "", fmt.Errorf("получены пустые данные от модуля 'ModuleCrypto'")
	}

	// fmt.Println("Логин: ", loginMQTT, "Пароль: ", passwordMQTT, "CA серт: ", ServerCaCert, "Сертификат: ", clientCert, "Ключ серта: ", clientKey) // Для отладки

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(ServerCaCert) {
		return nil, "", "", "", "", "", fmt.Errorf("не удалось добавить корневой (CA) сертификат")
	}

	certificate, err := tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		return nil, "", "", "", "", "", fmt.Errorf("не удалось загрузить пару ключей: %v", err)
	}

	// Создаёт TLS-конфигурацию
	tlsConfig := &tls.Config{
		RootCAs:            certPool,
		Certificates:       []tls.Certificate{certificate},
		InsecureSkipVerify: false, // Включает проверку подлинности сертификата сервера
	}

	// Очищает конфиденциальные данные после использования
	defer clearSensitive(ServerCaCert, clientCert, clientKey)

	return tlsConfig, string(urlBroker), string(portMQTT), string(loginMQTT), string(passwordMQTT), string(mqttID), nil
}

// Stop завершает MQTT-соединение
func (svc *MQTTService) Stop() {
	if svc.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond) // Таймаут плавного (корректного) отключения MQTT
		defer cancel()
		if err := svc.client.Disconnect(ctx); err != nil {
			log.Printf("Ошибка при отключении MQTT: %v", err)
		} else {
			log.Println("Клиент MQTT отключен")
		}
	}
}

// isAuthTxtIncomplete проверяет, является ли файл конфигурации auth.txt неполным
func isAuthTxtIncomplete() (bool, string) {
	exePath, err := os.Executable()
	if err != nil {
		return false, ""
	}
	cfgDir := filepath.Join(filepath.Dir(exePath), "config")
	txt := filepath.Join(cfgDir, "auth.txt")

	// Проверяет наличие auth.txt
	if _, err := os.Stat(txt); err != nil {
		// Возвращает результат, если файл не существует, так как возможно используется auth.enc
		return false, ""
	}

	// Чтение и парсинг данных по аналогии с ModuleCrypto.ParseAuthContent
	data, err := os.ReadFile(txt)
	if err != nil {
		return true, "Не удалось прочитать файл 'config\\auth.txt'. Исправьте и перезапустите службу."
	}

	serverURL := ""
	portMQTT := "8883"
	login := ""
	password := ""

	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key := ""
		val := ""
		if i := strings.Index(line, "="); i >= 0 {
			key = strings.ToLower(strings.TrimSpace(line[:i]))
			val = strings.TrimSpace(line[i+1:])
		} else {
			key = strings.ToLower(strings.TrimSpace(line))
			val = ""
		}
		switch key {
		case "serverurl":
			serverURL = val
		case "portmqtt":
			if val == "" {
				portMQTT = "8883"
			} else {
				portMQTT = val
			}
		case "loginmqtt":
			login = val
		case "passwordmqtt":
			password = val
		}
	}

	if serverURL == "" || portMQTT == "" || login == "" || password == "" {
		return true, "Обнаружен новый файл конфига 'auth.txt'. Заполните все поля в файле 'auth.txt'."
	}
	return false, ""
}

// logAuthIncompleteOnce запускает ModuleCrypto один раз для логирования статуса auth.txt
func logAuthIncompleteOnce() {
	authIncompleteLogOnce.Do(func() {
		baseTimeHex, err := GetBaseTimeHex()
		if err != nil {
			return
		}
		exePath, err := os.Executable()
		if err != nil {
			return
		}
		modulePath := filepath.Join(filepath.Dir(exePath), "ModuleCrypto.exe")

		cmd := exec.Command(modulePath, baseTimeHex, "full")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		// Достаточно запустить ModuleCrypto, чтобы он записал статус в свой лог
		_ = cmd.Run()
	})
}
