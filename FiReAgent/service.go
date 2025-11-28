// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"errors"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName        = "FiReAgent"                                                                                                   // Имя службы
	serviceDisplayName = "FiReAgent"                                                                                                   // Отображаемое имя службы
	serviceDescription = "Служба агента файловой ретрансляции для обработки запросов от сервера FiReMQ (Файловая ретрансляция и MQTT)" // Описание службы
)

// MyService реализует интерфейс svc.Handler для управления службой Windows
type MyService struct{}

// Execute выполняет основную логику службы Windows
func (m *MyService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// Использует мьютекс для предотвращения запуска второго экземпляра
	release, ok := acquireSingleInstance()
	if !ok {
		// Корректно завершается, поскольку другой экземпляр уже активен
		changes <- svc.Status{State: svc.StopPending}
		return
	}
	defer release()

	// Проверка наличия сертификата "CryptoAgent" в хранилище "Локальный компьютер\\Личное"
	if ok, err := isCryptoAgentCertInstalled(); err != nil {
		fmt.Println("Ошибка проверки сертификата 'CryptoAgent':", err)
		changes <- svc.Status{State: svc.StopPending}
		return
	} else if !ok {
		logMissingCertOnce() // Разово создаёт запись в логе ModuleCrypto

		fmt.Println("Сертификат с CN 'CryptoAgent' не найден. Установите CryptoAgent.pfx (Локальный компьютер\\Личное) и перезапустите службу.")
		changes <- svc.Status{State: svc.StopPending}
		return
	}

	// Проверка на незаполненный config\auth.txt — корректно останавливает службу (без перезапуска SCM)
	if stop, msg := isAuthTxtIncomplete(); stop {
		logAuthIncompleteOnce() // Разово создаёт запись в логе ModuleCrypto
		fmt.Println(msg)
		changes <- svc.Status{State: svc.StopPending}
		return
	}

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	// Запускает MQTT-клиент после успешной инициализации службы
	mqttSvc := StartMQTTClient()

	// Настраивает отправители отчетов, используя созданный MQTT клиент
	InitReportSenders(mqttSvc)

	// Старт планировщика обновлений (первая и последующие проверки только после истечения таймера)
	stopUpdater := StartClientUpdaterScheduler(mqttSvc)

	checkpoint := uint32(1)

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus

		case svc.Stop, svc.Shutdown:
			// Аккуратно гасит планировщик, чтобы он не запускал новые проверки
			stopUpdater()

			// Сообщает SCM, что началась остановка и она может занять время
			changes <- svc.Status{
				State:      svc.StopPending, // Служба переходит в состояние "остановка"
				Accepts:    cmdsAccepted,    // Подтверждает приём команд Stop/Shutdown
				CheckPoint: checkpoint,      // Обновляет контрольную точку для SCM (признак активности)
				WaitHint:   60000,           // Ожидаемое время следующего обновления (в мс)
			}
			checkpoint++

			// Запрещает новые операции и ждёт завершения активных
			stopDone := make(chan struct{})
			go func() {
				ok := mqttSvc.DrainActiveOperations(20 * time.Minute) // Ждёт до 20 мин. (когда FiReAgent работает как служба), чтобы активные задачи завершились перед остановкой
				if !ok {
					// Логирует, но двигается дальше (форс-стоп по таймауту)
					// log.Println("Таймаут ожидания завершения всех операций, выполняется принудительная остановка")
				}
				close(stopDone)
			}()

			ticker := time.NewTicker(5 * time.Second) // Таймер обновления статуса остановки для SCM
			defer ticker.Stop()
		WAIT:
			for {
				select {
				case <-stopDone:
					break WAIT
				case <-ticker.C:
					// Отправляет в SCM сигнал, что служба ещё в процессе остановки
					changes <- svc.Status{
						State:      svc.StopPending,
						Accepts:    cmdsAccepted,
						CheckPoint: checkpoint,
						WaitHint:   60000,
					}
					checkpoint++
				}
			}

			// Когда операции завершены (или по таймауту) — останавливает MQTT-клиент
			mqttSvc.Stop()
			return
		default:
			changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
		}
	}

	// Возвращает успешный код выхода, если выход не инициирован командой остановки
	return false, 0
}

// InstallService устанавливает службу FiReAgent в системе
func InstallService() {
	m, err := mgr.Connect()
	if err != nil {
		fmt.Println("Ошибка подключения SCM:", err)
		return
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		fmt.Println("Служба уже существует")
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Println("Ошибка пути к исполняемому файлу:", err)
		return
	}

	s, err = m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName: serviceDisplayName,
		StartType:   mgr.StartAutomatic,
		Description: serviceDescription,
	})
	if err != nil {
		fmt.Println("Ошибка создания службы:", err)
		return
	}
	defer s.Close()

	// Устанавливает политику восстановления
	if err := setServiceRecovery(s); err != nil {
		fmt.Println("Ошибка установки политики восстановления:", err)
	}

	// Не запускает службу, если обнаружен запущенный отладочный экземпляр, чтобы избежать конфликта mqttID
	if isAnotherInstanceRunning() {
		fmt.Println("Служба установлена, но не запущена, так как работает отладка (запустится при перезагрузке компьютера)")
		return
	}

	// Запускает службу немедленно, если отладочный экземпляр отсутствует
	fmt.Println("Служба установлена")
	if err := s.Start(); err != nil {
		fmt.Println("Ошибка запуска службы:", err)
		return
	}
	fmt.Println("Служба запущена")
}

// UninstallService удаляет службу FiReAgent из системы
func UninstallService() {
	m, err := mgr.Connect()
	if err != nil {
		fmt.Println("Ошибка подключения SCM:", err)
		return
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		// Предоставляет информативное сообщение, если служба не найдена
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			fmt.Println("Служба не установлена")
			return
		}
		fmt.Println("Ошибка открытия службы:", err)
		return
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		fmt.Println("Ошибка получения статуса службы:", err)
		return
	}

	if status.State == svc.Running || status.State == svc.StartPending || status.State == svc.ContinuePending {
		if _, err := s.Control(svc.Stop); err != nil {
			fmt.Println("Ошибка остановки службы:", err)
			return
		}

		fmt.Println("Корректная остановка службы, пожалуйста ожидайте...")

		// Дожидается реальной остановки
		ok := waitServiceStop(s, 3*time.Minute)
		if !ok {
			fmt.Println("Таймаут остановки службы (будет удалена при первой возможности)")
		} else {
			fmt.Println("Служба остановлена")
		}
	}

	if err := s.Delete(); err != nil {
		fmt.Println("Ошибка удаления службы:", err)
		return
	}
	fmt.Println("Служба удалена")
}

// waitServiceStop опрашивает статус службы до тех пор, пока она не перейдёт в состояние Stopped или не истечёт заданный таймаут
func waitServiceStop(s *mgr.Service, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err == nil && st.State == svc.Stopped {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// setServiceRecovery устанавливает политику восстановления службы
func setServiceRecovery(s *mgr.Service) error {
	actions := []windows.SC_ACTION{
		{Type: windows.SC_ACTION_RESTART, Delay: 0},
		{Type: windows.SC_ACTION_RESTART, Delay: 0},
		{Type: windows.SC_ACTION_RESTART, Delay: 0},
	}

	failureActions := windows.SERVICE_FAILURE_ACTIONS{
		ResetPeriod:  86400, // 1 день в секундах
		RebootMsg:    nil,
		Command:      nil,
		ActionsCount: uint32(len(actions)),
		Actions:      &actions[0],
	}

	h := windows.Handle(s.Handle)
	return windows.ChangeServiceConfig2(
		h,
		windows.SERVICE_CONFIG_FAILURE_ACTIONS,
		(*byte)(unsafe.Pointer(&failureActions)),
	)
}

// RunService запускает службу и ожидает команд остановки
func RunService() {
	if err := svc.Run(serviceName, &MyService{}); err != nil {
		fmt.Println("Ошибка запуска службы:", err)
		return
	}
	fmt.Println("Служба остановлена")
}

// isAnotherInstanceRunning проверяет наличие другого запущенного экземпляра программы
func isAnotherInstanceRunning() bool {
	const mutexName = "Global\\FiReAgent_Lock"
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, windows.StringToUTF16Ptr(mutexName))
	switch err {
	case nil:
		// Мьютекс существует, что указывает на наличие запущенного экземпляра
		_ = windows.CloseHandle(h)
		return true
	case windows.ERROR_FILE_NOT_FOUND, windows.ERROR_INVALID_HANDLE:
		// Мьютекс отсутствует, что означает отсутствие других экземпляров
		return false
	default:
		// Трактует любые другие ошибки как признак того, что экземпляр занят
		return true
	}
}
