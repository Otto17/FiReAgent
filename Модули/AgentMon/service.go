// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName        = "AgentMon"
	serviceDisplayName = "AgentMon"
	serviceDescription = "Мониторит и автоматически восстановливает службу FiReAgent, если она не запущена."
)

// WatchService реализует интерфейс svc.Handler для управления службой Windows
type WatchService struct{}

// Execute выполняет основную логику службы Windows
func (m *WatchService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// Мьютекс для предотвращения запуска второго экземпляра
	release, ok := acquireSingleInstance()
	if !ok {
		changes <- svc.Status{State: svc.StopPending}
		return
	}
	defer release()

	// Канал для остановки цикла мониторинга
	stopCh := make(chan struct{})
	go watchLoop(stopCh)

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus

		case svc.Stop, svc.Shutdown:
			close(stopCh)
			changes <- svc.Status{State: svc.StopPending}
			return
		}
	}

	return false, 0
}

// acquireSingleInstance обеспечивает глобальную защиту от дублирующего запуска службы
func acquireSingleInstance() (release func(), ok bool) {
	const mutexName = "Global\\AgentMon_Lock"

	h, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(mutexName))
	if err != nil {
		if err == windows.ERROR_ALREADY_EXISTS {
			_ = windows.CloseHandle(h)
			return nil, false
		}
		return nil, false
	}

	return func() {
		_ = windows.CloseHandle(h)
	}, true
}

// RunService запускает службу и ожидает команду остановки
func RunService() {
	_ = svc.Run(serviceName, &WatchService{})
}

// InstallService устанавливает службу AgentMon
func InstallService() {
	m, err := mgr.Connect()
	if err != nil {
		return
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err == nil {
		defer s.Close()

		status, err := s.Query()
		if err != nil {
			return
		}

		if status.State == svc.Running {
			return
		}

		_ = s.Start()
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		return
	}

	s, err = m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName: serviceDisplayName,
		StartType:   mgr.StartAutomatic,
		Description: serviceDescription,
	})
	if err != nil {
		return
	}
	defer s.Close()

	// Устанавливает политику восстановления
	_ = setServiceRecovery(s)

	_ = s.Start()
}

// UninstallService удаляет службу AgentMon
func UninstallService() {
	m, err := mgr.Connect()
	if err != nil {
		return
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return
	}

	if status.State == svc.Running || status.State == svc.StartPending || status.State == svc.ContinuePending {
		_, _ = s.Control(svc.Stop)
		waitServiceStop(s, 30*time.Second)
	}

	_ = s.Delete()
}

// waitServiceStop опрашивает статус службы до перехода в состояние Stopped или истечения таймаута
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
		{Type: windows.SC_ACTION_RESTART, Delay: 5000},  // Перезапуск через 5 сек
		{Type: windows.SC_ACTION_RESTART, Delay: 10000}, // Перезапуск через 10 сек
		{Type: windows.SC_ACTION_RESTART, Delay: 30000}, // Перезапуск через 30 сек
	}

	failureActions := windows.SERVICE_FAILURE_ACTIONS{
		ResetPeriod:  86400, // Сброс счётчика через 1 день
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
