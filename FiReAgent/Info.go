// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
)

// ReportSender управляет отправкой отчётов одного типа (Lite или Aida)
type ReportSender struct {
	Prefix         string        // Префикс отчёта (Lite или Aida)
	FirstDelay     time.Duration // Задержка перед первым запуском
	Interval       time.Duration // Интервал между запусками
	MQTTService    *MQTTService  // Сервис MQTT для публикации
	ExePath        string        // Полный путь к исполняемому файлу
	CurrentTimer   *time.Timer   // Текущий таймер планировщика
	ReportFileName string        // Имя создаваемого файла отчёта
	Topic          string        // MQTT-топик для публикации отчёта
	nextRun        time.Time     // Время следующего запланированного запуска
	timerLock      sync.Mutex    // Мьютекс для защиты доступа к таймеру
	reconnectCh    chan struct{} // Канал уведомления о восстановлении соединения
}

// getBaseName извлекает базовое имя из mqttID используя разделитель '_'
func getBaseName(mqttID string) string {
	parts := strings.SplitN(mqttID, "_", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return mqttID
}

// InitReportSenders инициализирует и запускает отправителей отчётов
func InitReportSenders(mqttSvc *MQTTService) {
	// Получает путь к текущему исполняемому файлу
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal("Ошибка получения пути к программе:", err)
	}

	// Создаёт директорию Reports если она ещё не существует
	reportsDir := filepath.Join(filepath.Dir(exePath), "Reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		log.Printf("Ошибка создания папки Reports: %v", err)
	}

	// Инициализация отправителя Lite-отчётов
	liteSender := &ReportSender{
		Prefix:      "Lite",
		FirstDelay:  10 * time.Second, // Запускается через 10 секунд для первоначальной отправки
		Interval:    2 * time.Hour,    // Устанавливает интервал повторных запусков в 2 часа
		MQTTService: mqttSvc,
		ExePath:     exePath,
		reconnectCh: make(chan struct{}, 1), // Инициализирует канал
	}
	liteSender.Start()
	// log.Println("Инициализирован отправитель Lite-отчётов")

	// Инициализация отправителя Aida-отчётов
	aidaSender := &ReportSender{
		Prefix:      "Aida",
		FirstDelay:  2*time.Minute + 10*time.Second, // Запускается через 2 минуты и 10 секунд
		Interval:    2 * time.Hour,                  // Устанавливает интервал повторных запусков в 2 часа
		MQTTService: mqttSvc,
		ExePath:     exePath,
		reconnectCh: make(chan struct{}, 1), // Инициализирует канал
	}
	aidaSender.Start()
	// log.Println("Инициализирован отправитель Aida-отчётов")

	// Сохраняет отправителей в MQTTService для управления переподключением
	mqttSvc.reportLock.Lock()
	defer mqttSvc.reportLock.Unlock()
	mqttSvc.liteSender = liteSender
	mqttSvc.aidaSender = aidaSender
}

// Start запускает начальный таймер для планирования отчётов
func (rs *ReportSender) Start() {
	// Определяет базовое имя клиента для использования в имени файла
	baseName := getBaseName(rs.MQTTService.mqttID)

	// Формирует уникальное имя файла и топик для публикации
	rs.ReportFileName = fmt.Sprintf("%s_%s.html.xz", rs.Prefix, baseName)
	rs.Topic = fmt.Sprintf("Client/ModuleInfo/%s/%s", rs.Prefix, rs.MQTTService.mqttID)

	// Фиксирует время следующего запуска для корректной обработки переподключения
	rs.nextRun = time.Now().Add(rs.FirstDelay)

	// Запускает таймер, который вызовет `runAndReschedule` после задержки
	rs.timerLock.Lock()
	defer rs.timerLock.Unlock()
	rs.CurrentTimer = time.AfterFunc(rs.FirstDelay, rs.runAndReschedule)
	// log.Printf("Запланирован %s-отчёт через %v", rs.Prefix, rs.FirstDelay)
}

// ScheduleNext планирует следующий запуск отчёта используя заданный интервал
func (rs *ReportSender) ScheduleNext() {
	rs.CurrentTimer = time.AfterFunc(rs.Interval, func() {
		rs.RunModule()
		rs.ScheduleNext()
	})
	// log.Printf("Следующий %s-отчёт через %v", rs.Prefix, rs.Interval)
}

// runAndReschedule запускает модуль отчёта и планирует следующий запуск
func (rs *ReportSender) runAndReschedule() {
	rs.timerLock.Lock()
	defer rs.timerLock.Unlock()

	select {
	case <-rs.reconnectCh: // Обрабатывает сигнал о восстановлении соединения, если он присутствует
		// log.Printf("Сработал триггер восстановления для %s-отчёта", rs.Prefix)
	default:
	}

	// Запускает отчёт только при наличии активного MQTT-соединения
	if rs.MQTTService.IsConnected() {
		rs.RunModule()
	} else {
		// log.Printf("Соединение отсутствует, %s-отчёт отложен", rs.Prefix)
	}

	// Обновляет время следующего запуска перед установкой таймера
	rs.nextRun = time.Now().Add(rs.Interval)
	rs.CurrentTimer = time.AfterFunc(rs.Interval, rs.runAndReschedule)
	// log.Printf("Следующий %s-отчёт через %v", rs.Prefix, rs.Interval)
}

// OnReconnect обрабатывает событие восстановления MQTT-соединения
func (rs *ReportSender) OnReconnect() {
	rs.timerLock.Lock()
	defer rs.timerLock.Unlock()

	// Проверяет, не пропущено ли время планового запуска во время дисконнекта
	if time.Now().After(rs.nextRun) {
		// Немедленно запускает отчет
		select {
		case rs.reconnectCh <- struct{}{}: // Отправляет сигнал для немедленного запуска отчёта в `runAndReschedule`
		default: // Если канал полон - пропускаем
		}

		// Останавливает текущий таймер перед немедленным запуском
		if rs.CurrentTimer != nil {
			rs.CurrentTimer.Stop()
		}
		go rs.runAndReschedule()
	} else {
		// Пересчитывает оставшееся время и перезапускает таймер
		if rs.CurrentTimer != nil {
			rs.CurrentTimer.Stop()
		}

		remaining := time.Until(rs.nextRun)
		rs.CurrentTimer = time.AfterFunc(remaining, rs.runAndReschedule)
		// log.Printf("Перезапуск таймера %s-отчёта. Осталось: %v", rs.Prefix, remaining)
	}
}

// RunModule запускает модуль "ModuleInfo.exe" для генерации отчёта
func (rs *ReportSender) RunModule() {
	// Если идёт остановка FiReAgent — новый сбор отчёта не стартует
	if rs.MQTTService.ops.IsStopping() {
		// log.Printf("Остановка в процессе, %s-отчёт не запускается", rs.Prefix)
		return
	}

	// log.Printf("Запуск модуля %s-отчёта", rs.Prefix)

	cmd := exec.Command(filepath.Join(filepath.Dir(rs.ExePath), "ModuleInfo.exe"), rs.Prefix)

	// Регистрация операции (включая генерацию + отправку отчёта)
	done, ok := rs.MQTTService.ops.Start()
	if !ok {
		// log.Printf("Остановка в процессе, %s-отчёт не запускается", rs.Prefix)
		return
	}

	if err := cmd.Start(); err != nil {
		done() // Закрытие операции при ошибке старта
		log.Printf("Ошибка запуска модуля %s: %v", rs.Prefix, err)
		return
	}

	// Ожидает завершения процесса в фоновом режиме чтобы не блокировать основной поток
	go func() {
		defer done()

		err := cmd.Wait()
		if err != nil {
			log.Printf("Ошибка выполнения модуля %s: %v", rs.Prefix, err)
			return
		}

		time.Sleep(500 * time.Millisecond) // Гарантия записи файла модулем
		rs.SendReport()
	}()
}

// SendReport находит сгенерированный отчёт, отправляет его и удаляет
func (rs *ReportSender) SendReport() {
	// Формирует полный путь к файлу отчёта внутри директории Reports
	filePath := filepath.Join(filepath.Dir(rs.ExePath), "Reports", rs.ReportFileName)

	// Проверяет существование файла отчёта
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Printf("Файл отчёта '%s' не найден", filePath)
		return
	}

	// Использует анонимную функцию для обеспечения корректного закрытия файла через defer
	if err := func() error {
		// Открывает файл
		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("ошибка открытия файла: %v", err)
		}
		defer file.Close()

		// Отправляет файл
		if err := rs.sendFileChunks(file); err != nil {
			return fmt.Errorf("ошибка отправки: %v", err)
		}
		return nil
	}(); err != nil {
		// log.Printf("%s-отчёт: %v", rs.Prefix, err)
		return
	}

	// Очищает локальное хранилище после успешной отправки
	if err := os.Remove(filePath); err != nil {
		log.Printf("Ошибка удаления файла: %v", err)
	}

	// log.Printf("%s-отчёт успешно отправлен и удален", rs.Prefix)
}

// sendFileChunks читает файл по частям и публикует их через MQTT с QoS 2
func (rs *ReportSender) sendFileChunks(file *os.File) error {
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("ошибка получения информации о файле: %v", err)
	}

	// Ограничивает максимальный размер файла для публикации
	if fileInfo.Size() > 8*1024*1024 {
		return fmt.Errorf("размер отчёта превышает 8МБ ( байт)")
	}

	// Присваивает уникальный идентификатор для сборки файла на сервере
	fileID := uuid.New()
	chunkSize := 4096 // 4KB на чанк
	buffer := make([]byte, chunkSize)
	chunkNum := uint64(0)
	totalChunks := uint64((fileInfo.Size() + int64(chunkSize) - 1) / int64(chunkSize)) // Корректное округление вверх

	// Исключает отправку пустых или некорректно созданных файлов
	if totalChunks == 0 {
		return fmt.Errorf("файл отчёта '%s' имеет нулевой размер. Отправка отменена", fileInfo.Name())
	}

	for {
		n, err := file.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("ошибка чтения файла: %v", err)
		}

		payload := preparePayload(fileID, chunkNum, totalChunks, buffer[:n])

		// ДЛЯ ОТЛАДКИ (проверка хэша каждой чанки)
		// hash := fmt.Sprintf("%x", md5.Sum(payload[34:]))
		// log.Printf("Чанк %d хеш: %s", chunkNum, hash)

		if _, err := rs.MQTTService.client.Publish(context.Background(), &paho.Publish{
			QoS:     2,
			Topic:   rs.Topic,
			Payload: payload,
		}); err != nil {
			return fmt.Errorf("ошибка отправки чанка %d: %v", chunkNum, err)
		}

		chunkNum++
	}

	// log.Printf("Файл %s успешно отправлен в топик %s (%d чанков)", rs.ReportFileName, rs.Topic, chunkNum)
	return nil
}

// preparePayload собирает бинарный payload включая метаданные файла и чанка
func preparePayload(fileID uuid.UUID, chunkNum, totalChunks uint64, data []byte) []byte {
	payload := make([]byte, 34+len(data)) // 2 байта флаги + 34 байта метаданные + данные
	var flags uint16
	if chunkNum == totalChunks-1 {
		flags |= 0x01 // Устанавливает флаг последнего чанка
	}

	// Записывает метаданные в формате Little Endian для совместимости
	binary.LittleEndian.PutUint16(payload[0:2], flags)
	copy(payload[2:18], fileID[:])
	binary.LittleEndian.PutUint64(payload[18:26], chunkNum)
	binary.LittleEndian.PutUint64(payload[26:34], totalChunks)
	copy(payload[34:], data)

	return payload
}
