// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const CurrentVersion = "01.11.25" // Текущая версия ModuleQUIC в формате "дд.мм.гг"

// ModuleData описывает структуру данных для получения всех параметров от FiReAgent
type ModuleData struct {
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

// Response описывает структуру для JSON-ответа
type Response struct {
	QUIC_Execution string `json:"QUIC_Execution"`        // Статус выполнения ("Успех" или "Ошибка")
	Attempts       string `json:"Attempts,omitempty"`    // Номер попытки скачивания файла
	Description    string `json:"Description,omitempty"` // Описание ошибки или успеха
	Answer         string `json:"Answer"`                // Дата и время окончания работы модуля
}

func main() {
	// Проверка флага версии
	if len(os.Args) >= 2 && strings.EqualFold(os.Args[1], "--version") {
		fmt.Printf("Версия \"ModuleQUIC\": %s\n", CurrentVersion)
		return
	}

	// Проверяет, что запуск был выполнен с необходимыми аргументами
	if len(os.Args) < 4 {
		fmt.Println("Недостаточно аргументов...")
		WriteToLogFile("Попытка запуска модуля с недостаточным количеством аргументов...")
		return
	}

	// Парсинг аргументов
	baseTimeHex := os.Args[1]
	// Проверяет, что аргументы соответствуют ожидаемому формату пайпа
	if os.Args[2] != "--pipe" || !strings.HasPrefix(os.Args[3], "--pipename=") {
		fmt.Println("Неверный формат аргументов")
		WriteToLogFile("Попытка запуска модуля с неверным форматом аргументов...")
		return
	}
	pipeName := `\\.\pipe\` + strings.TrimPrefix(os.Args[3], "--pipename=")

	// Проверка BaseTimeHex
	regBaseTimeHex, err := getBaseTimeHex()
	if err != nil {
		WriteToLogFile("Ошибка чтения реестра для получения BaseTime: %v", err)
		return
	}
	// Проверяет BaseTimeHex для подтверждения легитимности вызова
	if baseTimeHex != regBaseTimeHex {
		WriteToLogFile("Неверные данные в аргументе BaseTime!")
		return
	}

	// Создаёт именованный канал в режиме сервера
	ln, err := winio.ListenPipe(pipeName, nil)
	if err != nil {
		WriteToLogFile("Ошибка создания канала: %v", err)
		return
	}
	defer ln.Close()

	// Ожидает входящего подключения от клиента
	conn, err := ln.Accept()
	if err != nil {
		WriteToLogFile("Ошибка принятия подключения к каналу: %v", err)
		return
	}
	defer conn.Close()

	// Читает данные из канала, которые должны быть в формате [длина][данные]
	data, err := readPipeData(conn)
	if err != nil {
		WriteToLogFile("Ошибка чтения данных из канала: %v", err)
		return
	}

	// Демаршализирует входящий JSON в структуру ModuleData
	var moduleData ModuleData
	if err := json.Unmarshal(data, &moduleData); err != nil {
		WriteToLogFile("Ошибка разбора JSON: %v", err)
		return
	}

	// Очистка конфиденциальных данных при завершении
	defer func() {
		// Обнуляет логин пользователя
		if moduleData.UserName != "" {
			userBytes := []byte(moduleData.UserName)
			for i := range userBytes {
				userBytes[i] = 0
			}
			moduleData.UserName = ""
		}

		// Обнуляет пароль пользователя
		if moduleData.UserPassword != "" {
			passBytes := []byte(moduleData.UserPassword)
			for i := range passBytes {
				passBytes[i] = 0
			}
			moduleData.UserPassword = ""
		}

		// Обнуляет одноразовый токен
		if moduleData.Token != "" {
			tokenBytes := []byte(moduleData.Token)
			for i := range tokenBytes {
				tokenBytes[i] = 0
			}
			moduleData.Token = ""
		}

		// Обнуляет сертификаты в ОЗУ, чтобы предотвратить их утечку
		clearSensitive(moduleData.ServerCaCert, moduleData.ClientCert, moduleData.ClientKey)

		runtime.GC() // Принудительный сбор мусора для немедленной очистки
	}()

	// Подготавливает путь для загрузки, создавая необходимые директории и устанавливая права
	downloadPath, err := prepareDownloadPath(moduleData.DownloadRunPath)
	if err != nil {
		finalResp := createResponse("Ошибка", "0", err.Error())
		_ = writePipeData(conn, []byte(finalResp))
		WriteToLogFile("Ошибка подготовки пути: %v", err)
		return
	}
	moduleData.DownloadRunPath = downloadPath

	// Определяет, нужно ли устанавливать временное исключение Defender
	var tempExclusionPath string
	defFolder := `C:\ProgramData\FiReAgent`

	if strings.HasPrefix(strings.ToLower(downloadPath), strings.ToLower(`C:\ProgramData\FiReAgent\`)) {
		// Путь по умолчанию всегда добавляется в исключение
		if err := EnsureDefenderExclusion(defFolder); err != nil {
			WriteToLogFile("Предупреждение: не удалось гарантировать исключение %s: %v", defFolder, err)
		}
	} else {
		// Обработка произвольного пути
		if !moduleData.OnlyDownload && !moduleData.NotDeleteAfterInstallation {
			folder := filepath.Dir(downloadPath)
			// Пытается добавить временное исключение для произвольной папки, если файл будет запускаться и удаляться
			if err := EnsureDefenderExclusion(folder); err == nil {
				tempExclusionPath = folder
			} else {
				WriteToLogFile("Предупреждение: временное исключение не добавлено %s: %v", folder, err)
			}
		}
	}

	// Скачивание файла с сервера
	result := DownloadFile(moduleData.Token, moduleData.XXH3, moduleData.MqttID, moduleData.DownloadRunPath, moduleData.URL, moduleData.PortQUIC, moduleData.ServerCaCert, moduleData.ClientCert, moduleData.ClientKey)

	// Парсинг результата скачивания
	var resp Response
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		WriteToLogFile("Ошибка парсинга результата скачивания: %v", err)
		return
	}

	finalExecution := resp.QUIC_Execution
	finalAttempts := resp.Attempts
	finalDescription := resp.Description

	// Обрабатывает результат, если скачивание успешно
	if resp.QUIC_Execution == "Успех" {
		// Если флаг OnlyDownload не установлен, происходит запуск
		if !moduleData.OnlyDownload {
			// Запуск задания в планировщике и получение результата
			schedulerResult := CreateAndRunTask(&moduleData)

			// Обновляет финальный ответ в зависимости от результата выполнения задания
			if schedulerResult != "" {
				finalExecution = "Ошибка"
				finalDescription = schedulerResult
			} else {
				finalDescription = "Задача успешно выполнена"
				if moduleData.NotDeleteAfterInstallation {
					finalDescription += ", файл не удалён (флаг NotDeleteAfterInstallation)."
				} else {
					// Удаление файла после успешного выполнения
					if err := os.Remove(moduleData.DownloadRunPath); err != nil {
						WriteToLogFile("Ошибка удаления файла: %v", err)
						finalDescription += fmt.Sprintf(", ошибка удаления файла: %v", err)
					} else {
						finalDescription += ", файл успешно удалён."
					}
				}
			}

			// Удаляет временное исключение, если оно было добавлено ранее
			if tempExclusionPath != "" {
				if err := removeDefenderExclusionPS(tempExclusionPath); err != nil {
					WriteToLogFile("Ошибка удаления временного исключения %s: %v", tempExclusionPath, err)
				}
			}
		} else {
			// Если флаг OnlyDownload true, то файл скачан, но не запущен
			finalDescription = "Файл успешно скачан, без запуска."
		}
	}

	// Отправляет финальный результат обратно через канал
	finalResp := createResponse(finalExecution, finalAttempts, finalDescription)
	if err := writePipeData(conn, []byte(finalResp)); err != nil {
		WriteToLogFile("Ошибка отправки результата: %v", err)
		return
	}
	fmt.Printf("Результат отправлен: %s", finalResp)
}

// readPipeData читает бинарные данные из канала с префиксом длины
func readPipeData(conn io.Reader) ([]byte, error) {
	var length int32
	if err := binary.Read(conn, binary.LittleEndian, &length); err != nil {
		return nil, fmt.Errorf("ошибка чтения длины данных: %w", err)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("ошибка чтения данных: %w", err)
	}
	return data, nil
}

// writePipeData отправляет бинарные данные через канал с префиксом длины
func writePipeData(conn io.Writer, data []byte) error {
	length := int32(len(data))
	if err := binary.Write(conn, binary.LittleEndian, length); err != nil {
		return fmt.Errorf("ошибка записи длины данных: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("ошибка записи данных: %w", err)
	}
	return nil
}

// getBaseTimeHex получает значение BaseTime из реестра и переводит его в HEX
func getBaseTimeHex() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters`, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("не удалось открыть реестр: %w", err)
	}
	defer key.Close()

	baseTime, _, err := key.GetIntegerValue("BaseTime")
	if err != nil {
		return "", fmt.Errorf("не удалось получить данные из реестра: %w", err)
	}

	// Преобразует в шестнадцатеричный формат (8 символов, нижний регистр)
	return fmt.Sprintf("%08x", baseTime), nil
}

// createResponse создаёт JSON-ответ для передачи его по именованному каналу
func createResponse(execution, attempts, description string) string {
	// Устанавливает первую букву описания в верхний регистр
	if description != "" {
		runes := []rune(description)
		runes[0] = unicode.ToUpper(runes[0])
		description = string(runes)
	}

	resp := Response{
		QUIC_Execution: execution,
		Attempts:       attempts,
		Description:    description,
		Answer:         time.Now().Format("02.01.06(15:04:05)"),
	}
	jsonResp, err := json.Marshal(resp)
	if err != nil {
		return fmt.Sprintf("error: ошибка маршалинга JSON: %v", err)
	}
	return string(jsonResp)
}

// clearSensitive обнуляет чувствительные данные, такие как массивы байтов и сертификаты, в ОЗУ
func clearSensitive(args ...any) {
	for _, arg := range args {
		switch v := arg.(type) {
		case []byte: // Обнуляет байтовый массив
			for i := range v {
				v[i] = 0
			}
		case *tls.Certificate: // Обнуляет сертификат и приватный ключ
			for i := range v.Certificate {
				for j := range v.Certificate[i] {
					v.Certificate[i][j] = 0
				}
				v.Certificate[i] = nil
			}
			v.PrivateKey = nil
		}
	}
	runtime.GC() // Принудительный сбор мусора
}

// prepareDownloadPath создаёт путь и применяет ACL права
func prepareDownloadPath(downloadPath string) (string, error) {
	// Проверяет, что путь загрузки не пустой
	if downloadPath == "" {
		return "", fmt.Errorf("путь загрузки не указан")
	}

	var baseDir string
	var isDefaultPath bool
	if !filepath.IsAbs(downloadPath) {
		// Определяет базовую директорию, если указано только имя файла
		baseDir = `C:\ProgramData\FiReAgent\Files`
		isDefaultPath = true
	} else {
		// Проверяет абсолютный путь
		volume := filepath.VolumeName(downloadPath)
		if _, err := os.Stat(volume + `\\`); os.IsNotExist(err) {
			return "", fmt.Errorf("диск %s не существует", volume)
		}
		baseDir = filepath.Dir(downloadPath)
		isDefaultPath = false
	}

	var aclApplyPath string

	if isDefaultPath {
		// Особый случай для пути по умолчанию: права всегда применяются к 'C:\ProgramData\FiReAgent'
		aclApplyPath = `C:\ProgramData\FiReAgent`
	} else {
		// Для абсолютных путей ищет первую несуществующую директорию, чтобы применить ACL только к новой части
		pathToCheck := baseDir
		// firstNewDir будет содержать путь к самой верхней папке, которую предстоит создать
		var firstNewDir string

		for {
			_, err := os.Stat(pathToCheck)
			if err == nil {
				// Папка pathToCheck существует, найдена граница существующего пути
				break
			}
			if !os.IsNotExist(err) {
				// Обрабатывает ошибку, отличную от "не существует"
				return "", fmt.Errorf("ошибка проверки пути %s: %v", pathToCheck, err)
			}

			// pathToCheck не существует и является верхней папкой, которую нужно создать
			firstNewDir = pathToCheck

			parent := filepath.Dir(pathToCheck)
			if parent == pathToCheck {
				// Дошли до корня диска (например, "D:\")
				break
			}
			pathToCheck = parent
		}
		aclApplyPath = firstNewDir
	}

	// os.MkdirAll безопасно пропускает создание уже существующих папок
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", fmt.Errorf("не удалось создать директорию %s: %v", baseDir, err)
	}
	log.Printf("[Папка] Структура директорий до %s обеспечена.", baseDir)

	// Применяет права, если были найдены папки для создания
	if aclApplyPath != "" {
		if err := applyFullControlACL(aclApplyPath); err != nil {
			log.Printf("[Права] Ошибка при установке прав на %s: %v", aclApplyPath, err)
		} else {
			log.Printf("[Права] Права успешно добавлены для %s и вложенных объектов.", aclApplyPath)
		}
	} else {
		log.Printf("[Права] Путь %s уже полностью существовал. Применение прав не требуется.", baseDir)
	}

	// Возвращает полный путь к файлу
	if !filepath.IsAbs(downloadPath) {
		return filepath.Join(baseDir, downloadPath), nil
	}
	return downloadPath, nil
}

// applyFullControlACL добавляет полный доступ для предопределенных групп пользователей и системы
func applyFullControlACL(path string) error {
	// SIDs групп
	systemSID, err := windows.StringToSid("S-1-5-18") // СИСТЕМА (NT AUTHORITY\SYSTEM)
	if err != nil {
		return fmt.Errorf("SID СИСТЕМА: %v", err)
	}

	adminSID, err := windows.StringToSid("S-1-5-32-544") // Администраторы (Administrators)
	if err != nil {
		return fmt.Errorf("SID Администраторы: %v", err)
	}

	usersSID, err := windows.StringToSid("S-1-5-32-545") // Пользователи (Users)
	if err != nil {
		return fmt.Errorf("SID Пользователи: %v", err)
	}

	// Получает текущий DACL объекта, чтобы добавить новые записи, а не перезаписать существующие
	var currentDacl *windows.ACL
	securityInfo, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)

	if err != nil {
		log.Printf("[ACL] Предупреждение: не удалось получить текущий DACL для %s: %v (создаём новый)", path, err)
		// Если не удалось получить текущий DACL, продолжает с nil (создаётся новый)
		currentDacl = nil
	} else {
		// Получает DACL из дескриптора безопасности
		currentDacl, _, err = securityInfo.DACL()
		if err != nil {
			log.Printf("[ACL] Предупреждение: не удалось извлечь DACL из SecurityInfo для %s: %v (создаём новый)", path, err)
			currentDacl = nil
		}
	}

	// Формирует массив EXPLICIT_ACCESS для добавления новых прав
	ea := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(systemSID),
			},
		},
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(adminSID),
			},
		},
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(usersSID),
			},
		},
	}

	// Создаёт новый ACL, дополняя существующий currentDacl
	newDacl, err := windows.ACLFromEntries(ea, currentDacl)
	if err != nil {
		return fmt.Errorf("ACLFromEntries: %v", err)
	}

	// Устанавливает DACL для объекта (без PROTECTED флага, чтобы сохранить наследование)
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil, nil, newDacl, nil,
	)
	if err != nil {
		return fmt.Errorf("SetNamedSecurityInfo: %v", err)
	}

	log.Printf("[ACL] Успешно дополнен DACL для %s", path)

	// Рекурсивно обрабатывает поддиректории и файлы
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		log.Printf("[ACL] Ошибка чтения директории %s: %v (продолжаем)", path, err)
		return nil
	}

	for _, e := range entries {
		subPath := filepath.Join(path, e.Name())
		if err := applyFullControlACL(subPath); err != nil {
			log.Printf("[ACL] Ошибка применения ACL к %s: %v (продолжаем)", subPath, err)
			// Продолжает обработку остальных объектов, игнорируя ошибки
		}
	}

	return nil
}
