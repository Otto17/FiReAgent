// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	CurrentVersion = "01.11.25" // Текущая версия Uninstall в формате "дд.мм.гг"

	// Флаги MessageBox
	MB_YESNO        = 0x00000004 // Кнопки "Да" и "Нет"
	MB_ICONQUESTION = 0x00000020 // Иконка с вопросительным знаком

	// Коды возврата MessageBox
	IDYES = 6 // Нажата кнопка "Да"
	IDNO  = 7 // Нажата кнопка "Нет"

	// Флаги CreateProcess
	createBreakawayFromJob uint32 = 0x01000000 // Запускает процесс отдельно от родительского (не завершается при остановке службы)
	createNewProcessGroup  uint32 = 0x00000200 // Создаёт независимую группу процессов (изолирует управляющие сигналы)
)

var (
	user32          = syscall.NewLazyDLL("user32.dll") // Библиотеку user32.dll (графические функции Windows API)
	procMessageBoxW = user32.NewProc("MessageBoxW")    // Получение процедуры MessageBoxW (отображение простого диалогового окна)

	procProcessIdToSessionId = kernel32.NewProc("ProcessIdToSessionId") // Получение SessionID процесса
)

func main() {
	// Показывает версию Uninstall
	args := os.Args
	if len(args) >= 2 && strings.EqualFold(args[1], "--version") {
		fmt.Printf("Версия \"Uninstall\": %s\n", CurrentVersion)
		return
	}

	// Определение контекста: служба (Session 0) или интерактивный режим
	sess0 := isSession0()

	// Детачимся только если нас запустила служба (Session 0). При интерактивном запуске — не детачимся, чтобы не терять консоль
	if sess0 {
		// Отвязка от родительского процесса SCM, чтобы не погибнуть при остановке службы
		if err := tryBreakAwayFromJob(); err != nil {
			fmt.Fprintf(os.Stderr, "Предупреждение: не удалось отвязаться от родительского процесса: %v\n", err)
		}
	}

	force := hasArg("--force") // force проверяет наличие флага --force для принудительного удаления

	exePath, err := os.Executable()
	if err != nil {
		fmt.Println("Не удалось определить путь к исполняемому файлу:", err)
		return
	}
	targetDir := filepath.Dir(exePath)
	defFolder := `C:\ProgramData\FiReAgent`

	// Запрещает запуск, если текущая директория не FiReAgent
	if strings.ToLower(filepath.Base(targetDir)) != "fireagent" {
		fmt.Println("Запуск в текущей директории запрещён!")
		return
	}

	// Запрашивает подтверждение пользователя, если не активирован force-режим
	if !force {
		if !messageBoxYesNo("Удаление FiReAgent", "Вы уверены, что хотите удалить FiReAgent?") {
			return
		}
	}

	// Останавливает и удаляет службу FiReAgent, если она существует
	exists, running, _ := serviceExistsAndRunning("FiReAgent")
	if exists {
		if running {
			fmt.Println("Служба \"FiReAgent\" (запущена) — остановка и удаление службы...")
		} else {
			fmt.Println("Служба \"FiReAgent\" установлена (не запущена) — удаление службы...")
		}
		_ = stopAndDeleteServiceViaExe(targetDir)

		fmt.Println("Ожидание корректной остановки службы \"FiReAgent\"...")

		// Ожидает завершения процесса FiReAgent.exe для предотвращения блокировок
		waitFiReAgentGracefulExit(filepath.Join(targetDir, "FiReAgent.exe"))
	} else {
		fmt.Println("Служба \"FiReAgent\" не установлена.")
	}

	// Завершает процессы, блокирующие файлы для удаления
	fmt.Println("Проверка и завершение блокирующих процессов...")
	killBlockingProcesses(targetDir)

	// Удаляет пути из исключений Защитника Windows
	fmt.Println("Удаление исключений в Защитнике Windows (если были)...")
	for _, p := range []string{targetDir, defFolder} {
		if err := RemoveDefenderExclusion(p); err != nil {
			fmt.Printf("Предупреждение: не удалось удалить исключение \"%s\": %v\n", p, err)
		} else {
			fmt.Printf("Исключение удалено (если было): %s\n", p)
		}
	}

	// Создает отдельный процесс для удаления директорий во избежание самоблокировки
	parentDir := filepath.Dir(targetDir)
	_ = os.Chdir(parentDir)
	fmt.Println("Удаление папок \"FiReAgent\"...")
	createDeleteProcess(targetDir, defFolder)

	// Удаляет запись о программе из системного списка установленных приложений
	fmt.Println("Очистка реестра...")
	if err := unregisterUninstallEntry("FiReAgent"); err != nil {
		fmt.Println("Предупреждение: не удалось удалить запись uninstall:", err)
	}

	// Удаляет сертификат CryptoAgent из хранилища
	fmt.Println("Удаление сертификата \"CryptoAgent\"...")
	removeCryptoAgentCert()

	fmt.Println("Удаление \"FiReAgent\" успешно завершено!")

	// Ожидает нажатия Enter перед выходом, если не активирован force-режим
	if !force {
		fmt.Println("\n\nДля выхода нажмите Enter.")
		_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
	}

	os.Exit(0)
}

// tryBreakAwayFromJob перезапускает текущий процесс вне родительского SCM объекта
func tryBreakAwayFromJob() error {
	// Проверяет переменную окружения, чтобы избежать рекурсивного запуска
	if os.Getenv("UNINST_DETACHED") == "1" {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// Перезапускает себя с флагами breakaway; этот (родительский) процесс сразу завершится
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "UNINST_DETACHED=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createBreakawayFromJob | createNewProcessGroup,
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	os.Exit(0) // Исходный экземпляр завершается, новый продолжит работу независимо от службы
	return nil
}

// hasArg проверяет наличие указанного аргумента в командной строке
func hasArg(name string) bool {
	for _, a := range os.Args[1:] {
		if strings.EqualFold(a, name) {
			return true
		}
	}
	return false
}

// unregisterUninstallEntry удаляет запись о приложении из реестра Windows
func unregisterUninstallEntry(appName string) error {
	keyPath := `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + appName
	// Использует WOW64_64KEY для корректного удаления в 64-битных системах
	err := registry.DeleteKey(registry.LOCAL_MACHINE, keyPath)
	if err != nil {
		return fmt.Errorf("DeleteKey %s: %w", keyPath, err)
	}
	return nil
}

// waitFiReAgentGracefulExit ожидает корректного завершения процесса FiReAgent.exe до 20 минут, после — принудительное завершение
func waitFiReAgentGracefulExit(agentExe string) {
	const maxWait = 20 * time.Minute
	deadline := time.Now().Add(maxWait)
	printed := time.Time{}

	for time.Now().Before(deadline) {
		pids := findPIDsByPath(agentExe)

		// Если службы уже нет или она в состоянии Stopped — дополнительно убеждается, что процессов не осталось
		exists, state, _ := queryServiceState("FiReAgent")
		if (!exists || state == SERVICE_STOPPED) && len(pids) == 0 {
			fmt.Println("FiReAgent.exe завершился корректно.")
			return
		}

		// Периодический опрос статуса
		if time.Since(printed) >= 20*time.Second { // Выводит статус каждые 20 сек., чтобы не засорять консоль
			st := "unknown"
			switch state {
			case SERVICE_RUNNING:
				st = "RUNNING"
			case SERVICE_STOP_PENDING:
				st = "STOP_PENDING"
			case SERVICE_STOPPED:
				st = "STOPPED"
			}
			if !exists {
				st = "NOT_EXISTS"
			}
			fmt.Printf("Ждем остановки: статус службы=%s, активные PID: %v\n", st, pids)
			printed = time.Now()
		}
		time.Sleep(1 * time.Second) // Пауза 1сек. между опросами службы и процессов
	}

	// Таймаут - принудительное завершение
	pids := findPIDsByPath(agentExe)
	if len(pids) == 0 {
		fmt.Println("FiReAgent.exe завершился.")
		return
	}
	fmt.Printf("Таймаут ожидания — принудительное завершение: %v\n", pids)
	for _, pid := range pids {
		if terminatePID(pid) {
			fmt.Printf(" Завершен PID %d\n", pid)
		} else {
			fmt.Printf(" Не удалось завершить PID %d\n", pid)
		}
	}
}

// messageBoxYesNo отображает диалоговое окно Windows с кнопками Да и Нет
func messageBoxYesNo(caption, text string) bool {
	textPtr, _ := syscall.UTF16PtrFromString(text)
	captionPtr, _ := syscall.UTF16PtrFromString(caption)

	ret, _, _ := procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(captionPtr)),
		uintptr(MB_YESNO|MB_ICONQUESTION),
	)
	return ret == IDYES
}

// isSession0 возвращает true, если процесс запущен в сессии 0 (службы)
func isSession0() bool {
	pid := uint32(os.Getpid())
	var sid uint32
	r1, _, _ := procProcessIdToSessionId.Call(uintptr(pid), uintptr(unsafe.Pointer(&sid)))
	if r1 == 0 {
		return false
	}
	return sid == 0
}
