// Copyright (c) 2025 Otto
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

	"golang.org/x/text/encoding/charmap"
)

const (
	CurrentVersion = "01.11.25" // Текущая версия ClientUpdater в формате "дд.мм.гг"

	exeName         = "FiReAgent.exe"                                                 // Главный исполняемый файл агента, который будет останавливаться (служба) пред обновлением и запускаться после
	zipAssetPattern = `^FiReAgent-([0-9]{2}\.[0-9]{2}\.[0-9]{2})-windows-amd64\.zip$` // Шаблон имени ZIP-ассета релиза "FiReAgent-дд.мм.гг-windows-amd64.zip"
	tmpDirName      = "tmp"                                                           // Временная папка для загрузки, распаковки обновления срепозитория

	// Тайм-ауты (страховка от зависаний)
	httpTimeout  = 5 * time.Minute  // Чтобы скачивание не висело бесконечно при проблемах с сетью/репозиторием
	cmdTimeout   = 60 * time.Second // Чтобы вызовы FiReAgent -sd / -is не зависли навсегда (например, если служба подвисла)
	checkTimeout = 20 * time.Second // Ограничение времени запроса к API релизов (GitHub/GitFlic), предохранитель от зависаний

	baseDir = `C:\Program Files\FiReAgent` // Базовая директория, в которой производится обновление, выход за её пределы запрещён в целях безопасности

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

	// Запрет запуска вне каталога установки
	if !pathEqualFold(exeDir(), baseDir) {
		fmt.Printf("Запуск возможен только из каталога: \"%s\"\nТекущий каталог: %s\n",
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

	// Логирование (инициализация после проверки каталога и прав)
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

	// Читает историю обновлений для определения локальной версии
	hist, err := readUpdateHistory(conf.UpdateDir)
	if err != nil {
		// Предупреждение о неудачном чтении истории
		log.Printf("Предупреждение: не удалось прочитать update_history.json: %v", err)
	}
	localVer := strings.TrimSpace(hist.Last)
	if localVer == "" {
		localVer = "00.00.00"
	}

	// Проверяет наличие новой версии в репозитории
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	meta, trace, err := CheckLatest(ctx, conf.PrimaryRepo, conf.GHURL, conf.GFURL, conf.GFToken)
	if err != nil {
		fmt.Println("Обновлений нет.")
		return nil
	}
	if !isRemoteNewer(localVer, meta.RemoteVersion) {
		fmt.Println("Обновлений нет.")
		return nil
	}

	// Переключается на файловое логирование, поскольку обновление подтверждено
	ClientUpdaterLogging()
	defer LogBlankLines(2)

	// Теперь пишет подробности в лог
	log.Printf("Локальная версия (из JSON): %s", localVer)

	// Выгружает трассировку фоллбэков (если использовались)
	for _, msg := range trace {
		log.Printf("%s", msg)
	}
	log.Printf("Доступна новая версия: %s (ассет: %s)", meta.RemoteVersion, meta.AssetName)

	// Создаёт временную директорию и настраивает отложенное удаление
	tmpDir := filepath.Join(exeDir(), tmpDirName)
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Printf("Предупреждение: не удалось удалить tmp: %v", err)
		}
	}()
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать tmp: %w", err)
	}

	assetPath := filepath.Join(tmpDir, meta.AssetName)
	log.Printf("Скачивание: %s → %s", meta.AssetURL, assetPath)

	headers := map[string]string{}

	// Добавляет заголовок авторизации, если используется GitFlic и токен доступен
	if strings.EqualFold(meta.Repo, "gitflic") && strings.TrimSpace(conf.GFToken) != "" {
		headers["Authorization"] = "token " + strings.TrimSpace(conf.GFToken)
	}
	if err := downloadWithChecksum(meta.AssetURL, assetPath, meta.ExpectedSHA, headers); err != nil {
		return fmt.Errorf("скачивание не удалось: %w", err)
	}

	extractDir := filepath.Join(tmpDir, "unpacked")
	if err := unzipAll(assetPath, extractDir); err != nil {
		return fmt.Errorf("ошибка распаковки: %w", err)
	}
	log.Printf("Распаковано: %s", extractDir)

	// Загружает манифест обновления из распакованного архива
	man, err := loadManifest(filepath.Join(extractDir, "update.toml"))
	if err != nil {
		return fmt.Errorf("не удалось прочитать update.toml: %w", err)
	}
	if strings.TrimSpace(man.Version) != "" {
		log.Printf("Манифест версии: %s", man.Version)
	}

	// Останавливает службу и удаляет её (ключ "-sd")
	exePath := filepath.Join(baseDir, exeName)
	if err := runCmdTimeout(exePath, cmdTimeout, "-sd"); err != nil {
		log.Printf("Предупреждение: FiReAgent -sd завершился с ошибкой (возможно, служба не установлена): %v", err)
	} else {
		log.Printf("FiReAgent остановлен и служба удалена.")
	}

	// Применяет операции обновления, описанные в манифесте
	if err := applyOperations(extractDir, baseDir, man); err != nil {
		return fmt.Errorf("ошибка применения обновления: %w", err)
	}

	// Запускает агента и устанавливает службу (ключ "-is")
	if err := runCmdTimeout(exePath, cmdTimeout, "-is"); err != nil {
		return fmt.Errorf("не удалось запустить FiReAgent (-is): %w", err)
	}
	log.Printf("FiReAgent запущен (-is).")

	// Обновляет локальную историю обновлений
	if err := appendUpdateHistory(conf.UpdateDir, meta.RemoteVersion, meta.Repo); err != nil {
		log.Printf("Предупреждение: не удалось обновить историю: %v", err)
	}

	fmt.Println("Обновление выполнено успешно.")
	return nil
}

// -------------------------------------------------------------
// Вспомогательные функции
// -------------------------------------------------------------

// isRemoteNewer сравнивает две версии в формате "дд.мм.гг"
func isRemoteNewer(local, remote string) bool {
	lt, err1 := time.Parse("02.01.06", strings.TrimSpace(local))
	rt, err2 := time.Parse("02.01.06", strings.TrimSpace(remote))
	if err2 != nil {
		return false // Если удалённую версию не удалось распарсить, обновления нет
	}
	if err1 != nil {
		return true // Если локальную версию не удалось распарсить, считаем удаленную новее
	}
	return rt.After(lt)
}

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
	n := normalizeZipEntryName(f) // Использует нормализованное имя
	n = strings.ReplaceAll(n, "\\", "/")
	n = strings.TrimSpace(n)
	if n == "" {
		return "", false, false
	}

	clean := path.Clean(strings.TrimPrefix(n, "./"))
	clean = strings.Trim(clean, "/")

	// Запрещает обход каталогов (..) и небезопасные символы
	if clean == "" || clean == "." {
		return "", false, false
	}
	if strings.Contains(clean, "..") || strings.ContainsAny(clean, ":*?\"<>|") {
		return "", false, false
	}

	if strings.EqualFold(clean, "update.toml") {
		return filepath.FromSlash(clean), false, true
	}
	low := strings.ToLower(clean)

	// Разрешает только файлы внутри подкаталога "fireagent/" или "update.toml"
	if strings.HasPrefix(low, "fireagent/") {
		// Определяет "директорность" по нормализованному 'n'
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

	// Принимает имя как есть, если оно похоже на валидный UTF-8
	if utf8.ValidString(n) {
		return n
	}

	// Иначе пробует декодировать "сырые" байты имени
	raw := []byte(n)

	candidates := make([]string, 0, 3)

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

// scoreCyr подсчитывает количество символов, похожих на кириллицу или служебные символы пути
func scoreCyr(s string) int {
	cnt := 0
	for _, r := range s {
		if (r >= 0x0400 && r <= 0x04FF) || r == ' ' || r == '.' || r == '_' || r == '-' || (r >= '0' && r <= '9') {
			cnt++
		}
	}
	return cnt
}

// pathEqualFold сравнивает два пути без учета регистра после очистки
func pathEqualFold(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
