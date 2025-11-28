// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/text/encoding/charmap"
)

// CreateAndRunTask является единой точкой входа для определения ОС
func CreateAndRunTask(data *ModuleData) string {
	// Определяет версию Windows.
	isWin10, err := isWindows10OrGreater()
	if err != nil {
		WriteToLogFile("Не удалось определить версию Windows, считаем как Win10+: %v", err)
		isWin10 = true
	}

	if isWin10 {
		// Для Win10+ создаёт задачу через COM-логику.
		return createAndRunTaskCOM(data)
	}
	// Для Win 8.1 генерирует XML и импортирует его через системную утилиту "schtasks"
	return createAndRunTaskXMLWin8(data)
}

// ---- WINDOWS 10+ (COM ЛОГИКА) ----

// createAndRunTaskCOM создаёт и выполняет задачу через COM-интерфейс
func createAndRunTaskCOM(data *ModuleData) string {
	// Инициализирует COM
	ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
	defer ole.CoUninitialize()

	// Создаёт объект планировщика задач
	unknown, err := oleutil.CreateObject("Schedule.Service")
	if err != nil {
		WriteToLogFile("Ошибка создания объекта планировщика: %v", err)
		return "ошибка создания объекта планировщика"
	}
	defer unknown.Release()

	// Получает интерфейс IDispatch у COM-объекта "Schedule.Service"
	taskSvc, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		WriteToLogFile("Ошибка получения интерфейса: %v", err)
		return "ошибка получения интерфейса планировщика"
	}
	defer taskSvc.Release()

	// Подключается к службе планировщика
	if _, err := oleutil.CallMethod(taskSvc, "Connect"); err != nil {
		WriteToLogFile("Ошибка подключения к планировщику: %v", err)
		return "ошибка подключения к планировщику"
	}

	// Получает корневую папку задач планировщика
	folder := oleutil.MustCallMethod(taskSvc, "GetFolder", `\`).ToIDispatch()
	defer folder.Release()

	// Удаляет старые задачи
	if err := cleanupOldTasks(folder); err != nil {
		WriteToLogFile("Ошибка очистки старых задач: %v", err)
	}

	// Создаёт задачу
	taskDef := oleutil.MustCallMethod(taskSvc, "NewTask", 0).ToIDispatch()
	defer taskDef.Release()

	// Настраивает задачу
	if err := configureTask(taskDef, data); err != nil {
		return err.Error()
	}

	// Формирует имя задачи
	taskName := fmt.Sprintf("FiReMQ_QUIC_%s", time.Now().Format("02.01.06(15.04.05)"))

	// Регистрирует задачу
	if err := registerTask(folder, taskDef, taskName, data); err != nil {
		return err.Error()
	}

	// Запускает и отслеживает задачу
	task, err := runAndMonitorTask(folder, taskName)
	if err != nil {
		return err.Error()
	}
	defer task.Release()

	// Удаляет задачу
	if _, err := oleutil.CallMethod(folder, "DeleteTask", taskName, 0); err != nil {
		WriteToLogFile("Ошибка удаления задачи: %v", err)
	}

	return ""
}

// configureTask конфигурирует задачу (COM)
func configureTask(taskDef *ole.IDispatch, data *ModuleData) error {
	settings := oleutil.MustGetProperty(taskDef, "Settings").ToIDispatch()
	defer settings.Release()

	// Настройка совместимости планировщика
	oleutil.MustPutProperty(settings, "Compatibility", 6) // TASK_COMPATIBILITY: 6 = V2_4 (Настроить для: Windows 10+)

	// Настройка параметров питания.
	oleutil.MustPutProperty(settings, "StopIfGoingOnBatteries", false)     // Снимает галочку "Останавливать, при переходе на питание от батареи"
	oleutil.MustPutProperty(settings, "DisallowStartIfOnBatteries", false) // Снимает галочку "Запускать только при питании от электросети"
	oleutil.MustPutProperty(settings, "StartWhenAvailable", true)          // Немедленно запускать задачу, если пропущен плановый запуск

	// Настройка времени выполнения (8 часов)
	oleutil.MustPutProperty(settings, "ExecutionTimeLimit", "PT8H")

	// Настройка параметров простоя
	idleSettings := oleutil.MustGetProperty(settings, "IdleSettings").ToIDispatch()
	defer idleSettings.Release()
	oleutil.MustPutProperty(idleSettings, "StopOnIdleEnd", false) // Снимает галочку "Останавливать при выходе компьютера из простоя"

	// Настройка информации о задаче
	regInfo := oleutil.MustGetProperty(taskDef, "RegistrationInfo").ToIDispatch()
	defer regInfo.Release()
	oleutil.MustPutProperty(regInfo, "Description", "Выполнение задачи из FiReMQ от модуля 'ModuleQUIC'.") // Описание задачи
	oleutil.MustPutProperty(regInfo, "Author", "FiReMQ System")                                            // Автор задачи

	// Настройка триггера (однократный запуск при создании задачи)
	triggers := oleutil.MustGetProperty(taskDef, "Triggers").ToIDispatch()
	defer triggers.Release()

	// Создаёт триггер типа "При регистрации" (TASK_TRIGGER_REGISTRATION = 7)
	registrationTrigger := oleutil.MustCallMethod(triggers, "Create", 7).ToIDispatch()
	defer registrationTrigger.Release()

	// Устанавливает задержку в 1 секунду (формат ISO 8601)
	oleutil.MustPutProperty(registrationTrigger, "Delay", "PT1S")

	// Настройка действия
	actions := oleutil.MustGetProperty(taskDef, "Actions").ToIDispatch()
	defer actions.Release()
	action := oleutil.MustCallMethod(actions, "Create", 0).ToIDispatch() // 0 = TASK_ACTION_EXEC
	defer action.Release()

	// Определяет расширение файла
	ext := strings.ToLower(filepath.Ext(data.DownloadRunPath))
	var path, args string

	switch ext {
	case ".msi":
		// *.msi файлы запускается через системную утилиту "msiexec.exe"
		path = "msiexec.exe"
		args = fmt.Sprintf(`/i "%s" %s`, data.DownloadRunPath, data.ProgramRunArguments)
	case ".ps1":
		// PowerShell(x64) через системную утилиту "powershell.exe"
		path = "powershell.exe"
		args = fmt.Sprintf(`-ExecutionPolicy Bypass -NoProfile -File "%s" %s`, data.DownloadRunPath, data.ProgramRunArguments)
	default:
		// *.exe, *.bat, *.cmd и всё остальное
		path = data.DownloadRunPath
		args = data.ProgramRunArguments
	}

	// Устанавливает в задачу итоговый Path и Arguments
	oleutil.MustPutProperty(action, "Path", path)
	if strings.TrimSpace(args) != "" {
		oleutil.MustPutProperty(action, "Arguments", args)
	}

	// Устанавливает рабочую папку
	workDir := filepath.Dir(data.DownloadRunPath)
	oleutil.MustPutProperty(action, "WorkingDirectory", workDir)

	// Настройка параметров безопасности
	principal := oleutil.MustGetProperty(taskDef, "Principal").ToIDispatch()
	defer principal.Release()

	// Устанавливает уровень привилегий
	runLevel := 0 // TASK_RUNLEVEL_LUA (без повышенных прав)
	if data.RunWithHighestPrivileges {
		runLevel = 1 // TASK_RUNLEVEL_HIGHEST (с повышенными правами)
	}
	oleutil.MustPutProperty(principal, "RunLevel", runLevel)

	return nil
}

// registerTask регистрирует задачу (COM)
func registerTask(folder, taskDef *ole.IDispatch, taskName string, data *ModuleData) error {
	// Определяет тип входа и учётные данные
	var logonType int = 3 // TASK_LOGON_INTERACTIVE_TOKEN
	var userId, password any = nil, nil

	// Обрабатывает сценарии входа
	switch {
	case data.RunWhetherUserIsLoggedOnOrNot && (isSystemUser(data.UserName)):
		// Сценарий 1: Запуск от имени "СИСТЕМА"
		userId = "SYSTEM"
		logonType = 5 // TASK_LOGON_SERVICE_ACCOUNT
	case data.RunWhetherUserIsLoggedOnOrNot && !isSystemUser(data.UserName) && data.UserName != "":
		// Сценарий 2: Запуск для всех пользователей
		userId = data.UserName
		password = data.UserPassword
		logonType = 1 // TASK_LOGON_PASSWORD
	default:
		// Сценарий 3: Только для вошедших пользователей
		if data.UserName != "" && !isSystemUser(data.UserName) {
			userId = data.UserName
		}
		logonType = 3 // TASK_LOGON_INTERACTIVE_TOKEN
	}

	// Регистрирует задачу
	_, err := oleutil.CallMethod(
		folder,
		"RegisterTaskDefinition",
		taskName,
		taskDef,
		6, // TASK_CREATE_OR_UPDATE
		userId,
		password,
		logonType,
		nil, // sddl (вариант: nil)
	)

	// Обрабатывает код ошибки "0x80020009" (неверный логин или пароль) при регистрации
	if err != nil {
		if oleErr, ok := err.(*ole.OleError); ok && oleErr.Code() == 0x80020009 {
			return fmt.Errorf("неверный логин или пароль при создании задачи")
		}
		return fmt.Errorf("ошибк�� регистрации задачи: %v", err)
	}
	return nil
}

// isWindows10OrGreater определяет, что ОС — Windows 10 или выше
func isWindows10OrGreater() (bool, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()

	if major, _, err := k.GetIntegerValue("CurrentMajorVersionNumber"); err == nil {
		return major >= 10, nil
	}
	if verStr, _, err := k.GetStringValue("CurrentVersion"); err == nil {
		parts := strings.SplitN(verStr, ".", 2)
		if len(parts) > 0 {
			if maj, err := strconv.Atoi(parts[0]); err == nil {
				return maj >= 10, nil
			}
		}
	}
	return false, fmt.Errorf("не удалось определить версию Windows")
}

// ---- WINDOWS 8.1 (XML + SCHTASKS) ----

// createAndRunTaskXMLWin8 создаёт и выполняет задачу через XML + schtasks
func createAndRunTaskXMLWin8(data *ModuleData) string {
	taskName := fmt.Sprintf("FiReMQ_QUIC_%s", time.Now().Format("02.01.06(15.04.05)"))

	// Подготавливает XML
	xml, err := buildTaskXML(data)
	if err != nil {
		WriteToLogFile("Ошибка генерации XML задачи: %v", err)
		return "ошибка генерации XML задачи"
	}

	// Записывает XML во временный файл (UTF-16LE)
	xmlPath, err := writeUTF16LETempXML(xml)
	if err != nil {
		WriteToLogFile("Ошибка сохранения XML задачи: %v", err)
		return "ошибка сохранения XML задачи"
	}
	defer os.Remove(xmlPath)

	// Инициализирует COM для очистки/мониторинга
	ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
	defer ole.CoUninitialize()

	// Подключается к планировщику (для очистки и последующего мониторинга задачи)
	unknown, err := oleutil.CreateObject("Schedule.Service")
	if err != nil {
		WriteToLogFile("Ошибка создания объекта планировщика: %v", err)
		return "ошибка создания объекта планировщика"
	}
	defer unknown.Release()

	// Получает интерфейс IDispatch у COM-объекта "Schedule.Service"
	taskSvc, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		WriteToLogFile("Ошибка получения интерфейса: %v", err)
		return "ошибка получения интерфейса планировщика"
	}
	defer taskSvc.Release()

	// Подключается к службе планировщика
	if _, err := oleutil.CallMethod(taskSvc, "Connect"); err != nil {
		WriteToLogFile("Ошибка подключения к планировщику: %v", err)
		return "ошибка подключения к планировщику"
	}

	// Получает корневую папку задач планировщика
	folder := oleutil.MustCallMethod(taskSvc, "GetFolder", `\`).ToIDispatch()
	defer folder.Release()

	// Удаляет старые задачи
	if err := cleanupOldTasks(folder); err != nil {
		WriteToLogFile("Оши��ка очистки старых задач: %v", err)
	}

	// Импортирует через утилиту "schtasks"
	if err := importTaskViaSchtasks(xmlPath, taskName, data); err != nil {
		WriteToLogFile("Ошибка импорта задачи через schtasks: %v", err)
		return err.Error()
	}

	// Запускает + мониторинг
	task, err := runAndMonitorTask(folder, taskName)
	if err != nil {
		return err.Error()
	}
	defer task.Release()

	// Удаляет задачу
	if _, err := oleutil.CallMethod(folder, "DeleteTask", taskName, 0); err != nil {
		WriteToLogFile("Ошибка удаления задачи: %v", err)
	}

	return ""
}

// importTaskViaSchtasks импортирует задачу в планировщик через системную утилиту "schtasks.exe"
func importTaskViaSchtasks(xmlPath, taskName string, data *ModuleData) error {
	args := []string{"/Create", "/TN", taskName, "/XML", xmlPath, "/F"}

	// Подбирает RU/RP
	switch {
	case data.RunWhetherUserIsLoggedOnOrNot && isSystemUser(data.UserName):
		// SYSTEM
		args = append(args, "/RU", `NT AUTHORITY\SYSTEM`)
	case data.RunWhetherUserIsLoggedOnOrNot && !isSystemUser(data.UserName) && data.UserName != "":
		// Требуются логин/пароль
		args = append(args, "/RU", data.UserName, "/RP", data.UserPassword)
	default:
		// Интерактивный режим — если задан пользователь, подставим его (без /RP)
		if data.UserName != "" && !isSystemUser(data.UserName) {
			args = append(args, "/RU", data.UserName)
		}
		// Иначе без RU (будет как в XML)
	}

	// Запускает системную утилиту
	cmd := exec.Command("schtasks.exe", args...)
	out, err := cmd.CombinedOutput()
	msg := decodeOEM866(out)

	// Обрабатывает ошибки
	if err != nil {
		msg := strings.TrimSpace(string(msg))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("не удалось импортировать задачу (schtasks): %s", msg)
	}
	return nil
}

// buildTaskXML строит XML задачи под Win 8.1
func buildTaskXML(data *ModuleData) (string, error) {
	// Настройка совместимости планировщика
	taskVersion := "1.4" // Windows 8.1

	// Установка уровня привилегий
	runLevel := "LeastPrivilege" // Без повышенных прав
	if data.RunWithHighestPrivileges {
		runLevel = "HighestAvailable" // С повышенными правами
	}

	// Кому запускать
	isSys := data.RunWhetherUserIsLoggedOnOrNot && isSystemUser(data.UserName)

	principalID := "Author" // Автор задачи
	userId := ""
	logonType := "" // ВАЖНО: для SYSTEM оставляем пустым

	// Обработка сценариев входа.
	switch {
	case isSys:
		// Сценарий 1: Запуск от имени "СИСТЕМА"
		principalID = "System"
		userId = "S-1-5-18" // LocalSystem (SYSTEM)
	case data.RunWhetherUserIsLoggedOnOrNot && data.UserName != "" && !isSystemUser(data.UserName):
		// Сценарий 2: Запуск для всех пользователей
		userId = data.UserName
		logonType = "Password"
	default:
		// Сценарий 3: Только для вошедших пользователей
		if data.UserName != "" && !isSystemUser(data.UserName) {
			userId = data.UserName
		}
		logonType = "InteractiveToken"
	}

	// Экранирование.
	cmd := xmlEscape(data.DownloadRunPath)
	args := xmlEscape(data.ProgramRunArguments)
	workDir := xmlEscape(filepath.Dir(data.DownloadRunPath))
	desc := xmlEscape("Выполнение задачи из FiReMQ от модуля 'ModuleQUIC'.")
	author := xmlEscape("FiReMQ System")

	principalUser := ""
	if strings.TrimSpace(userId) != "" {
		// Вставка тега <UserId>, если пользователь задан
		principalUser = "<UserId>" + xmlEscape(userId) + "</UserId>"
	}
	logonTypeXml := ""
	if !isSys && strings.TrimSpace(logonType) != "" {
		// Указание типа входа (Password/InteractiveToken), кроме SYSTEM
		logonTypeXml = "<LogonType>" + logonType + "</LogonType>"
	}

	// Формирование окончательного XML
	xml := fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-16"?>
<Task version="%s" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
<RegistrationInfo>
<Date>%s</Date>
<Author>%s</Author>
<Description>%s</Description>
</RegistrationInfo>
<Triggers>
<RegistrationTrigger>
<Delay>PT1S</Delay>
<Enabled>true</Enabled>
</RegistrationTrigger>
</Triggers>
<Principals>
<Principal id="%s">
%s
%s
<RunLevel>%s</RunLevel>
</Principal>
</Principals>
<Settings>
<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
<AllowHardTerminate>true</AllowHardTerminate>
<StartWhenAvailable>true</StartWhenAvailable>
<RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
<IdleSettings>
<StopOnIdleEnd>false</StopOnIdleEnd>
<RestartOnIdle>false</RestartOnIdle>
</IdleSettings>
<AllowStartOnDemand>true</AllowStartOnDemand>
<Enabled>true</Enabled>
<Hidden>false</Hidden>
<RunOnlyIfIdle>false</RunOnlyIfIdle>
<WakeToRun>false</WakeToRun>
<ExecutionTimeLimit>PT8H</ExecutionTimeLimit>
<Priority>7</Priority>
</Settings>
<Actions Context="%s">
<Exec>
<Command>%s</Command>
%s
<WorkingDirectory>%s</WorkingDirectory>
</Exec>
</Actions>
</Task>`,
		taskVersion,
		time.Now().Format(time.RFC3339),
		author,
		desc,
		principalID,
		principalUser,
		logonTypeXml, // будет пустым для SYSTEM
		runLevel,
		principalID, // Context совпадает с id Principal
		cmd,
		func() string {
			if strings.TrimSpace(args) == "" {
				return ""
			}
			return "<Arguments>" + args + "</Arguments>"
		}(),
		workDir,
	)

	return xml, nil
}

// xmlEscape безопасно экранирует спецсимволы для XML
func xmlEscape(s string) string {
	r := strings.ReplaceAll(s, "&", "&amp;")
	r = strings.ReplaceAll(r, "<", "&lt;")
	r = strings.ReplaceAll(r, ">", "&gt;")
	r = strings.ReplaceAll(r, `"`, "&quot;")
	r = strings.ReplaceAll(r, "'", "&apos;")
	return r
}

// writeUTF16LETempXML пишет строку в UTF-16LE с BOM во временный .xml файл
func writeUTF16LETempXML(s string) (string, error) {
	// Кодирует в UTF-16LE с BOM
	utf16Data := utf16.Encode([]rune(s))
	buf := &bytes.Buffer{}
	// BOM
	buf.Write([]byte{0xFF, 0xFE})
	for _, r := range utf16Data {
		lo := byte(r & 0xFF)
		hi := byte((r >> 8) & 0xFF)
		buf.WriteByte(lo)
		buf.WriteByte(hi)
	}

	f, err := os.CreateTemp("", "FiReMQ_Task_*.xml")
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := f.Write(buf.Bytes()); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// decodeOEM866 декодирует OEM866
func decodeOEM866(b []byte) string {
	// Для CP866, которую использует cmd.exe/schtasks в консоли.
	dec := charmap.CodePage866.NewDecoder()
	s, _ := dec.Bytes(b)
	return string(s)
}

// ---- ОБЩИЕ ФУНКЦИИ ----

// cleanupOldTasks удаляет старые задачи из планировщика
func cleanupOldTasks(folder *ole.IDispatch) error {
	// Получает коллекцию задач
	tasks := oleutil.MustCallMethod(folder, "GetTasks", 0).ToIDispatch()
	defer tasks.Release()

	// Получает количество задач
	count := int(oleutil.MustGetProperty(tasks, "Count").Val)

	// Перебирает все задачи, удаляя старые
	for i := 1; i <= count; i++ {
		taskItem := oleutil.MustGetProperty(tasks, "Item", i).ToIDispatch()
		defer taskItem.Release()

		// Имя задачи
		taskName := oleutil.MustGetProperty(taskItem, "Name").ToString()

		if strings.HasPrefix(taskName, "FiReMQ_QUIC") {
			// Состояние задачи
			state := int(oleutil.MustGetProperty(taskItem, "State").Val)

			// 3 = TASK_STATE_READY
			if state == 3 {
				if _, err := oleutil.CallMethod(folder, "DeleteTask", taskName, 0); err != nil {
					WriteToLogFile("Ошибка удаления задачи '%s': %v", taskName, err)
				}
			}
		}
	}
	return nil
}

// runAndMonitorTask запускает и отслеживает задачу
func runAndMonitorTask(folder *ole.IDispatch, taskName string) (*ole.IDispatch, error) {
	task := oleutil.MustCallMethod(folder, "GetTask", taskName).ToIDispatch()
	if _, err := oleutil.CallMethod(task, "Run", nil); err != nil {
		return nil, fmt.Errorf("ошибка запуска задачи: %v", err)
	}

	fmt.Println("Ожидание завершения задачи...")

	for {
		time.Sleep(3 * time.Second)
		state := oleutil.MustGetProperty(task, "State").Val
		// 0: Unknown, 1: Disabled, 2: Queued, 3: Ready, 4: Running
		if state != 4 {
			break
		}
	}
	return task, nil
}

// isSystemUser проверяет, является ли имя "СИСТЕМА/NT AUTHORITY\SYSTEM"
func isSystemUser(name string) bool {
	n := strings.TrimSpace(strings.ToUpper(name))
	return n == "" || n == "СИСТЕМА" || n == "SYSTEM" || n == "NT AUTHORITY\\SYSTEM"
}
