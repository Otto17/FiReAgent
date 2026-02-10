// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"golang.org/x/text/encoding/charmap"
)

const (
	CurrentVersion = "10.02.25" // Текущая версия ClientUpdater в формате "дд.мм.гг"

	exeName         = "FiReAgent.exe"                                                 // Главный исполняемый файл агента, который будет останавливаться (служба) пред обновлением и запускаться после
	zipAssetPattern = `^FiReAgent-([0-9]{2}\.[0-9]{2}\.[0-9]{2})-windows-amd64\.zip$` // Шаблон имени ZIP-ассета релиза "FiReAgent-дд.мм.гг-windows-amd64.zip"
	tmpDirName      = "tmp"                                                           // Временная папка для загрузки, распаковки обновления с репозитория

	// Тайм-ауты (страховка от зависаний)
	httpTimeout  = 5 * time.Minute  // Чтобы скачивание не висело бесконечно при проблемах с сетью/репозиторием
	cmdTimeout   = 60 * time.Second // Чтобы вызовы FiReAgent -sd / -is не зависли навсегда (например, если служба подвисла)
	checkTimeout = 20 * time.Second // Ограничение времени запроса к API релизов (GitHub/GitFlic), предохранитель от зависаний

	baseDir = `C:\Program Files\FiReAgent` // Базовая директория, в которой производится обновление, выход за её пределы запрещён в целях безопасности

	agentMonExeName     = "AgentMon.exe" // Исполняемый файл службы мониторинга AgentMon
	agentMonServiceName = "AgentMon"     // Имя службы мониторинга

	// Флаги CreateProcess
	createBreakawayFromJob uint32 = 0x01000000 // Запускает процесс отдельно от родительского (не завершается при остановке службы)
	createNewProcessGroup  uint32 = 0x00000200 // Создаёт независимую группу процессов (изолирует управляющие сигналы)

)

func main() {
	// Отображает версию ClientUpdater
	if len(os.Args) >= 2 && strings.EqualFold(os.Args[1], "--version") {
		fmt.Printf("Версия \"ClientUpdater\": %s\n", CurrentVersion)
		return
	}

	// Запрет запуска вне папки установки
	if !pathEqualFold(exeDir(), baseDir) {
		fmt.Printf("Запуск возможен только из папки: \"%s\"\nТекущая папка: %s\n",
			baseDir, exeDir())
		return
	}

	// Получение прав администратора (UAC), если требуется
	if err := ensureElevated(); err != nil {
		if errors.Is(err, errRelaunchingElevated) {
			return // Возвращает управление, поскольку исходный процесс уже завершается
		}
		fmt.Printf("Не удалось получить права администратора: %v\n", err)
		return
	}

	// Отвязка от родительского процесса SCM, чтобы не погибнуть при остановке службы
	if err := tryBreakAwayFromJob(); err != nil {
		// Логгер еще не инициализирован, использует stderr для вывода предупреждений
		fmt.Fprintf(os.Stderr, "Предупреждение: не удалось отвязаться от родительского процесса: %v\n", err)
	}

	// Логирование (инициализация после проверки папки и прав)
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[ClientUpdater] ")

	// Загрузка конфига
	conf, err := loadOrCreateConf()
	if err != nil {
		log.Fatalf("Ошибка конфигурации: %v", err)
	}

	// Основная логика работы обновления
	if err := run(conf); err != nil {
		log.Printf("Ошибка: %v", err)
		os.Exit(1)
	}
}

// tryBreakAwayFromJob перезапускает текущий процесс вне родительского SCM объекта
func tryBreakAwayFromJob() error {
	// Проверяет переменную окружения, чтобы избежать рекурсивного запуска
	if os.Getenv("CU_DETACHED") == "1" {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// Перезапускает себя с флагами breakaway; этот (родительский) процесс сразу завершится
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "CU_DETACHED=1")
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

// -------------------------------------------------------------
// Основной процесс обновления
// -------------------------------------------------------------

// run выполняет основную логику проверки и применения обновления
func run(conf UpdaterConf) error {
	// Проверяет, что запуск происходит из базовой директории
	if !pathEqualFold(exeDir(), baseDir) {
		return fmt.Errorf("ClientUpdater должен запускаться только из %q", baseDir)
	}

	// Временная папка для работы
	tmpDir := filepath.Join(exeDir(), tmpDirName)

	// Определяет путь к FiReAgent (нужен для defer ниже)
	exePath := filepath.Join(baseDir, exeName)

	// Запуск FiReAgent ВСЕГДА перед завершением (независимо от наличия обновлений и ошибок)
	defer func() {
		// Проверяет наличие файла самообновления и запускает планировщик при любом исходе
		myExe, _ := os.Executable()
		newExe := strings.TrimSuffix(myExe, ".exe") + "_new.exe"
		if _, statErr := os.Stat(newExe); statErr == nil {
			log.Println("Запуск планировщика самообновления (замена ClientUpdater.exe после выхода)...")
			scheduleSelfUpdate(newExe, myExe)
		}

		log.Println("Инициализация запуска FiReAgent (-is)...")
		// Попытка запустить службу
		if err := runCmdTimeout(exePath, cmdTimeout, "-is"); err != nil {
			log.Printf("КРИТИЧЕСКАЯ ОШИБКА: Не удалось перезапустить FiReAgent: %v", err)
		} else {
			log.Printf("FiReAgent успешно запущен (-is).")
		}

		// Проверка и запуск службы AgentMon
		ensureAgentMonRunning()

		// Удаление tmp папки в самом конце
		time.Sleep(200 * time.Millisecond)
		if err := removeTmpDir(tmpDir); err != nil {
			log.Printf("Предупреждение: не удалось удалить tmp в конце работы: %v", err)
		}
	}()

	// Читает историю обновлений для определения локальной (текущей) версии
	hist, err := readUpdateHistory(conf.UpdateDir)
	if err != nil {
		// Предупреждение о неудачном чтении JSON истории
		log.Printf("Предупреждение: не удалось прочитать \"update_history.json\": %v", err)
	}
	localVer := strings.TrimSpace(hist.Last)
	if localVer == "" {
		localVer = "00.00.00"
	}

	// Проверяет наличие новых версий (возвращает список)
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	// Получает список обновлений
	updates, trace, err := CheckUpdates(ctx, localVer, conf.PrimaryRepo, conf.GHURL, conf.GFURL, conf.GFToken)
	if err != nil || len(updates) == 0 {
		// Если список пуст или ошибка - просто выходит, обновлений нет
		fmt.Println("Обновлений нет.")
		return nil
	}

	// Переключение на файловое логирование
	ClientUpdaterLogging()
	defer LogBlankLines(2)

	log.Printf("Локальная версия: %s. Найдено обновлений: %d", localVer, len(updates))

	// Выгружает трассировку
	for _, msg := range trace {
		log.Printf("%s", msg)
	}

	// Выводит план обновлений
	for i, u := range updates {
		log.Printf("  %d. Версия %s (от %s)", i+1, u.RemoteVersion, u.Repo)
	}

	// ЭТАП 1: Остановка службы (Один раз перед всеми обновлениями)
	if err := runCmdTimeout(exePath, cmdTimeout, "-sd"); err != nil {
		log.Printf("Предупреждение: FiReAgent -sd завершился с ошибкой (возможно, служба не установлена): %v", err)
	} else {
		log.Printf("FiReAgent остановлен и служба удалена.")
	}

	// ЭТАП 2: Поэтапная установка версий
	for i, meta := range updates {
		log.Printf(">>> Установка обновления %d из %d: версия %s <<<", i+1, len(updates), meta.RemoteVersion)

		// Очистка временной папки перед каждым этапом
		if err := removeTmpDir(tmpDir); err != nil {
			log.Printf("Предупреждение: ошибка очистки tmp перед версией %s: %v", meta.RemoteVersion, err)
		}
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			return fmt.Errorf("не удалось создать tmp: %w", err)
		}

		// Скачивание релиза
		assetPath := filepath.Join(tmpDir, meta.AssetName)
		log.Printf("Скачивание: %s", meta.AssetURL)
		headers := map[string]string{}
		if strings.EqualFold(meta.Repo, "gitflic") && strings.TrimSpace(conf.GFToken) != "" {
			headers["Authorization"] = "token " + strings.TrimSpace(conf.GFToken)
		}
		if err := downloadWithChecksum(meta.AssetURL, assetPath, meta.ExpectedSHA, headers); err != nil {
			return fmt.Errorf("ошибка скачивания версии %s: %w", meta.RemoteVersion, err)
		}

		// Распаковка архива
		extractDir := filepath.Join(tmpDir, "unpacked")
		if err := unzipAll(assetPath, extractDir); err != nil {
			return fmt.Errorf("ошибка распаковки версии %s: %w", meta.RemoteVersion, err)
		}

		// Поиск манифеста
		updateRoot, err := findUpdateRoot(extractDir)
		if err != nil {
			return fmt.Errorf("update.toml не найден в версии %s: %w", meta.RemoteVersion, err)
		}
		man, err := loadManifest(filepath.Join(updateRoot, "update.toml"))
		if err != nil {
			return fmt.Errorf("ошибка чтения манифеста версии %s: %w", meta.RemoteVersion, err)
		}

		// Применение обновления
		if _, err := applyOperations(updateRoot, baseDir, man); err != nil {
			return fmt.Errorf("сбой установки версии %s: %w", meta.RemoteVersion, err)
		}

		// Обновление истории (после каждого успешного шага)
		if err := appendUpdateHistory(conf.UpdateDir, meta.RemoteVersion, meta.Repo); err != nil {
			log.Printf("Предупреждение: не удалось обновить историю для %s: %v", meta.RemoteVersion, err)
		}

		// Обновление реестра (после каждого успешного шага)
		if err := updateRegistryVersion(meta.RemoteVersion); err != nil {
			log.Printf("Предупреждение: не удалось обновить реестр для %s: %v", meta.RemoteVersion, err)
		} else {
			log.Printf("Реестр Windows обновлён: %s", meta.RemoteVersion)
		}

		log.Printf("Версия %s успешно установлена.", meta.RemoteVersion)
		LogBlankLines(1)
	}

	fmt.Println("Все обновления выполнены успешно.")
	return nil // Сработает defer, запустив FiReAgent и удалит tmp
}

// -------------------------------------------------------------
// Вспомогательные функции
// -------------------------------------------------------------

// runCmdTimeout выполняет команду с указанным тайм-аутом, захватывая вывод
func runCmdTimeout(path string, timeout time.Duration, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()

	// Проверяет, что ошибка вызвана именно таймаутом контекста
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("таймаут команды: %s %v", path, args)
	}
	if err != nil {
		return fmt.Errorf("%v (вывод: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// -------------------------------------------------------------
// Распаковка ZIP с поддержкой кириллицы и фильтрацией путей
// -------------------------------------------------------------

// unzipAll распаковывает ZIP-архив в указанную директорию, используя фильтрацию и нормализацию имен
func unzipAll(zipPath, dst string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		rel, isDir, ok := sanitizeZipEntry(f)

		// Пропускает записи, не прошедшие проверку безопасности или фильтрацию
		if !ok {
			continue
		}
		dest := filepath.Join(dst, rel)
		if isDir {
			if err := os.MkdirAll(dest, 0755); err != nil {
				return err
			}
			continue
		}

		// Создаёт директории для файлов
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		tmp := dest + ".tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			_ = os.Remove(tmp)
			return err
		}
		out.Close()
		rc.Close()
		_ = os.Remove(dest)

		// Использует временный файл и переименовая для обеспечения атомарности и перезаписи файлов
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return nil
}

// sanitizeZipEntry фильтрует записи ZIP-архива, разрешая только безопасные пути и файлы агента
func sanitizeZipEntry(f *zip.File) (rel string, isDir bool, ok bool) {
	n := normalizeZipEntryName(f) // Использование нормализованного имени
	n = strings.ReplaceAll(n, "\\", "/")
	n = strings.TrimSpace(n)
	if n == "" {
		return "", false, false
	}

	clean := path.Clean(strings.TrimPrefix(n, "./"))
	clean = strings.Trim(clean, "/")

	// Запрещает обход папок (..) и небезопасные символы
	if clean == "" || clean == "." {
		return "", false, false
	}
	if strings.Contains(clean, "..") || strings.ContainsAny(clean, ":*?\"<>|") {
		return "", false, false
	}

	low := strings.ToLower(clean)
	parts := strings.Split(low, "/")

	// Паттерн 1: update.toml в корне
	if len(parts) == 1 && parts[0] == "update.toml" {
		return filepath.FromSlash(clean), false, true
	}

	// Паттерн 2: корневая_папка/update.toml (архив с корневой директорией)
	if len(parts) == 2 && parts[1] == "update.toml" {
		return filepath.FromSlash(clean), false, true
	}

	// Паттерн 3: fireagent/... в корне
	if len(parts) >= 1 && parts[0] == "fireagent" {
		isDir = strings.HasSuffix(n, "/") || f.FileInfo().IsDir()
		return filepath.FromSlash(clean), isDir, true
	}

	// Паттерн 4: корневая_папка/fireagent/... (архив с корневой директорией)
	if len(parts) >= 2 && parts[1] == "fireagent" {
		isDir = strings.HasSuffix(n, "/") || f.FileInfo().IsDir()
		return filepath.FromSlash(clean), isDir, true
	}

	return "", false, false
}

// normalizeZipEntryName преобразует имя файла из ZIP-архива в корректный UTF-8, используя несколько кодировок
func normalizeZipEntryName(f *zip.File) string {
	n := f.Name
	n = strings.TrimLeft(n, "/\\")
	if n == "" {
		return n
	}

	// Если строка валидный UTF-8 и содержит кириллические символы - используется как есть
	if utf8.ValidString(n) && hasCyrillicRunes(n) {
		return n
	}

	// Если строка валидный UTF-8 и содержит только ASCII - используется как есть
	if utf8.ValidString(n) && isASCII(n) {
		return n
	}

	// Иначе пробует декодировать из разных кодировок
	raw := []byte(n)
	candidates := make([]string, 0, 4)

	// Добавляет исходную строку как кандидата
	candidates = append(candidates, n)

	// Пытается декодировать байты, используя основные кодировки для кириллицы
	if b, err := charmap.Windows1251.NewDecoder().Bytes(raw); err == nil {
		candidates = append(candidates, string(b))
	}
	if b, err := charmap.CodePage866.NewDecoder().Bytes(raw); err == nil {
		candidates = append(candidates, string(b))
	}
	if b, err := charmap.CodePage437.NewDecoder().Bytes(raw); err == nil {
		candidates = append(candidates, string(b))
	}

	// Выбирает лучшую кандидатуру по количеству распознанных кириллических символов
	best := n
	bestScore := -1
	for _, s := range candidates {
		sc := scoreCyr(s)
		if sc > bestScore {
			best = s
			bestScore = sc
		}
	}
	return best
}

// hasCyrillicRunes проверяет, содержит ли строка кириллические символы
func hasCyrillicRunes(s string) bool {
	for _, r := range s {
		if r >= 0x0400 && r <= 0x04FF {
			return true
		}
	}
	return false
}

// isASCII проверяет, содержит ли строка только ASCII-символы
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// scoreCyr подсчитывает количество русских букв и служебных символов пути
func scoreCyr(s string) int {
	cnt := 0
	for _, r := range s {
		// Основные Русские буквы А-Я (0x0410-0x042F) и а-я (0x0430-0x044F)
		if r >= 0x0410 && r <= 0x044F {
			cnt += 2 // Больший вес для основных Русских букв
		} else if r == 0x0401 || r == 0x0451 {
			cnt += 2 // Ё и ё тоже основные Русские буквы
		} else if r >= 0x0400 && r <= 0x04FF {
			cnt++ // Меньший вес для остальной кириллицы
		} else if r == ' ' || r == '.' || r == '_' || r == '-' || (r >= '0' && r <= '9') {
			cnt++
		}
	}
	return cnt
}

// pathEqualFold сравнивает два пути без учета регистра после очистки
func pathEqualFold(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// removeTmpDir удаляет временную tmp папку
func removeTmpDir(tmpDir string) error {
	// Проверяет, существует ли папка
	info, err := os.Stat(tmpDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.Remove(tmpDir)
	}

	// Собирает все пути для удаления
	var files []string
	var dirs []string

	_ = filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || path == tmpDir {
			return nil
		}
		if info.IsDir() {
			dirs = append(dirs, path)
		} else {
			files = append(files, path)
		}
		return nil
	})

	// Удаляет все файлы
	for _, f := range files {
		for range 3 {
			if err := os.Remove(f); err == nil || os.IsNotExist(err) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Удаляет папки в обратном порядке (сначала вложенные)
	for i := len(dirs) - 1; i >= 0; i-- {
		for range 3 {
			if err := os.Remove(dirs[i]); err == nil || os.IsNotExist(err) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Удаляет корневую tmp папку
	for range 5 {
		if err := os.Remove(tmpDir); err == nil || os.IsNotExist(err) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}

	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("не удалось удалить папку: %s", tmpDir)
}

// findUpdateRoot ищет "update.toml" в распакованной директории и возвращает путь к корню обновления
func findUpdateRoot(extractDir string) (string, error) {
	// Сначала проверяет корень
	tomlPath := filepath.Join(extractDir, "update.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return extractDir, nil
	}

	// Ищет в подпапках первого уровня
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subDir := filepath.Join(extractDir, entry.Name())
		tomlPath := filepath.Join(subDir, "update.toml")
		if _, err := os.Stat(tomlPath); err == nil {
			return subDir, nil
		}
	}

	return "", fmt.Errorf("update.toml не найден в %s и его подпапках", extractDir)
}

// -------------------------------------------------------------
// Проверка и запуск службы AgentMon
// -------------------------------------------------------------

// isServiceRunning проверяет, запущена ли указанная служба Windows
func isServiceRunning(serviceName string) bool {
	m, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return false // Служба не существует или нет доступа
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return false
	}

	return status.State == svc.Running
}

// ensureAgentMonRunning проверяет, запущена ли служба AgentMon, и запускает её при необходимости
func ensureAgentMonRunning() {
	// Проверяет, запущена ли служба AgentMon
	if isServiceRunning(agentMonServiceName) {
		log.Printf("Служба %s уже запущена.", agentMonServiceName)
		return
	}

	log.Printf("Служба %s не запущена, попытка запуска...", agentMonServiceName)

	// Путь к исполняемому файлу AgentMon
	agentMonPath := filepath.Join(baseDir, agentMonExeName)

	// Проверяет существование файла
	if _, err := os.Stat(agentMonPath); os.IsNotExist(err) {
		log.Printf("Предупреждение: %s не найден: %s", agentMonExeName, agentMonPath)
		return
	}

	// Запускает AgentMon с ключом -is
	if err := runCmdTimeout(agentMonPath, cmdTimeout, "-is"); err != nil {
		log.Printf("Предупреждение: не удалось запустить %s: %v", agentMonServiceName, err)
	} else {
		log.Printf("Служба %s успешно запущена (-is).", agentMonServiceName)
	}
}
