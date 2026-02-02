// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// ApplyOperations применяет операции обновления и удаления из манифеста
// Возвращает true, если запланировано самообновление (создан файл _new.exe)
func applyOperations(extractDir, baseDir string, man *Manifest) (bool, error) {
	fiRoot := filepath.Join(extractDir, "FiReAgent")

	log.Printf("Применение манифеста: %d операций", len(man.Files))
	var updatedCount, deletedCount, skippedDeleteCount int

	// Путь к текущему исполняемому файлу для детекта самообновления
	myExe, _ := os.Executable()
	selfUpdatePending := false

	for _, it := range man.Files {
		// Проверяет, является ли целевой файл текущим апдейтером
		isSelfUpdate := false

		destAbs, err := resolveDest(baseDir, orDefault(it.Dest, it.Src))
		if err == nil && myExe != "" && strings.EqualFold(filepath.Clean(destAbs), filepath.Clean(myExe)) {
			isSelfUpdate = true
		}

		switch it.Action {
		case ActUpdate:
			srcRel := filepath.FromSlash(strings.TrimLeft(it.Src, `/\`))
			if srcRel == "" {
				return selfUpdatePending, fmt.Errorf("files.update: не задан Src")
			}
			srcAbs := filepath.Clean(filepath.Join(fiRoot, srcRel))

			// Проверяет, что источник находится внутри распакованной папки FiReAgent
			rootClean := filepath.Clean(fiRoot)
			if !strings.EqualFold(rootClean, srcAbs) && !strings.HasPrefix(strings.ToLower(srcAbs)+string(os.PathSeparator), strings.ToLower(rootClean)+string(os.PathSeparator)) {
				return selfUpdatePending, fmt.Errorf("src вне FiReAgent/: %s", it.Src)
			}

			if err != nil {
				return selfUpdatePending, err
			}

			// Вычисляет относительные пути для записи в лог
			srcLog := filepath.ToSlash(strings.TrimPrefix(srcAbs, fiRoot+string(os.PathSeparator)))
			destLog := destAbs
			if rel, err := filepath.Rel(baseDir, destAbs); err == nil {
				destLog = filepath.ToSlash(rel)
			}

			// Обработка папок
			if it.IsDir {
				srcInfo, statErr := os.Stat(srcAbs)
				if statErr != nil {
					return selfUpdatePending, fmt.Errorf("папка-источник не найден: %s: %w", srcAbs, statErr)
				}
				if !srcInfo.IsDir() {
					return selfUpdatePending, fmt.Errorf("IsDir=true, но источник не является папкой: %s", srcAbs)
				}

				// Подсчитывает количество файлов в папке для лога
				fileCount := countFilesInDir(srcAbs)

				if it.Replace {
					log.Printf("ЗАМЕНА ПАПКИ: %s -> %s (%d файлов)", srcLog, destLog, fileCount)
				} else {
					log.Printf("ОБНОВЛЕНИЕ ПАПКИ: %s -> %s (%d файлов)", srcLog, destLog, fileCount)
				}

				if err := copyDirReplace(srcAbs, destAbs, it.Replace); err != nil {
					return selfUpdatePending, fmt.Errorf("ошибка применения обновления папки %s -> %s: %w", srcAbs, destAbs, err)
				}

				updatedCount++
				log.Printf("УСПЕХ: %s (папка)", destLog)
				time.Sleep(20 * time.Millisecond)
				continue
			}

			// Получает размер файла для отображения в логе
			var size int64 = -1
			if inf, err := os.Stat(srcAbs); err == nil {
				size = inf.Size()
			}

			// Логика самообновления
			if isSelfUpdate {
				// Распаковывает с именем "ClientUpdater_new.exe"
				newName := strings.TrimSuffix(destAbs, ".exe") + "_new.exe"
				_ = os.Remove(newName) // Удаляет старый _new если есть

				// Копирует файл
				info, err := os.Stat(srcAbs)
				if err != nil {
					return selfUpdatePending, err
				}
				if err := copyFile(srcAbs, newName, info.Mode()); err != nil {
					return selfUpdatePending, fmt.Errorf("ошибка подготовки самообновления: %w", err)
				}

				log.Printf("САМООБНОВЛЕНИЕ: новая версия сохранена как %s. Будет применена при выходе.", filepath.Base(newName))
				selfUpdatePending = true
				updatedCount++
				continue
			}

			if size >= 0 {
				log.Printf("ОБНОВЛЕНИЕ: %s -> %s (%s)", srcLog, destLog, formatSize(size))
			} else {
				log.Printf("ОБНОВЛЕНИЕ: %s -> %s", srcLog, destLog)
			}

			if err := copyReplace(srcAbs, destAbs); err != nil {
				return selfUpdatePending, fmt.Errorf("ошибка применения обновления: обновлено %s -> %s: %w", srcAbs, destAbs, err)
			}

			updatedCount++
			log.Printf("УСПЕХ: %s", destLog)

			// Небольшая задержка помогает предотвратить блокировки антивирусами или программами индексации
			time.Sleep(20 * time.Millisecond)

		case ActDelete:
			destAbs, err := resolveDest(baseDir, it.Dest)
			if err != nil {
				return selfUpdatePending, err
			}

			// Защита от удаления самого себя
			if isSelfUpdate {
				log.Printf("ПРОПУСК удаления апдейтера: %s", destAbs)
				skippedDeleteCount++
				continue
			}

			destLog := destAbs
			if rel, err := filepath.Rel(baseDir, destAbs); err == nil {
				destLog = filepath.ToSlash(rel)
			}

			// Проверяет существование файла до удаления для корректного логирования результата
			_, statErr := os.Stat(destAbs)
			existed := statErr == nil

			if existed {
				log.Printf("УДАЛЕНИЕ: %s", destLog)
			} else {
				log.Printf("УДАЛЕНИЕ: %s (не найден, пропуск)", destLog)
			}

			if err := deletePath(destAbs); err != nil {
				return selfUpdatePending, fmt.Errorf("ошибка применения обновления: удалён %s: %w", destAbs, err)
			}

			if existed {
				deletedCount++
				log.Printf("УСПЕХ: %s (удалён)", destLog)
			} else {
				skippedDeleteCount++
				log.Printf("УСПЕХ: %s (пропуск)", destLog)
			}

		default:
			return selfUpdatePending, fmt.Errorf("неизвестный Action: %s", it.Action)
		}
	}

	log.Printf("Сводка: обновлено=%d, удалено=%d, пропущено удалений=%d", updatedCount, deletedCount, skippedDeleteCount)
	return selfUpdatePending, nil
}

// OrDefault возвращает строку, если она не пуста, иначе возвращает значение по умолчанию
func orDefault(v, def string) string {
	s := strings.TrimSpace(v)
	if s != "" {
		return s
	}
	return def
}

// CopyReplace копирует файл из src в dst, используя временный файл для атомарной замены
func copyReplace(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	// Использует несколько попыток на случай, если целевой файл временно занят другой программой
	var lastErr error
	for attempt := range 5 {
		tmp := dst + ".tmp"
		if err := copyFile(src, tmp, info.Mode()); err != nil {
			return err
		}
		_ = os.Remove(dst)
		if err := os.Rename(tmp, dst); err == nil {
			return nil
		} else {
			lastErr = err
		}
		_ = os.Remove(tmp)

		// После второй неудачной попытки пробует определить и завершить блокирующий процесс
		if attempt >= 1 {
			if tryUnlockFile(dst, 3) {
				// Файл разблокирован, сразу пробует ещё раз
				continue
			}
		}

		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("не удалось заменить файл: %s (последняя ошибка: %v)", dst, lastErr)
}

// CopyFile копирует содержимое файла
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		// Удаляет частичный файл в случае ошибки копирования
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// DeletePath удаляет файл или папку, включая рекурсивное удаление папок
func deletePath(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return os.RemoveAll(p)
	}

	// Использует несколько попыток на случай, если файл временно занят другой программой
	for range 5 {
		if err := os.Remove(p); err == nil || os.IsNotExist(err) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("не удалось удалить: %s", p)
}

// scheduleSelfUpdate запускает скрытый CMD процесс, который ждет завершения текущего PID, а затем заменяет oldExe на newExe (move /y)
func scheduleSelfUpdate(newExe, oldExe string) {
	pid := os.Getpid()

	// Формирование команды: ждёт PID, если PID нет -> перемещает new поверх old, "move /y" перезаписывает целевой файл
	cleanCmd := fmt.Sprintf(`move /y "%s" "%s"`, newExe, oldExe)

	// Ждёт завершения процесса (PID) в цикле (раз в секунду), затем выполняет подмену
	cmdLine := fmt.Sprintf(
		`cmd /C "for /l %%i in (0,0,1) do (timeout /t 1 /nobreak >nul & tasklist /fi "PID eq %d" | findstr %d >nul || (%s & exit))"`,
		pid, pid, cleanCmd,
	)

	// Настраивает параметры для скрытого запуска процесса через WinAPI
	si := &syscall.StartupInfo{Cb: uint32(unsafe.Sizeof(syscall.StartupInfo{})), Flags: 0x1, ShowWindow: 0}
	pi := &syscall.ProcessInformation{}
	cmdLinePtr, _ := syscall.UTF16PtrFromString(cmdLine)

	const CREATE_NO_WINDOW = 0x08000000 // Запускает процесс без создания окна
	syscall.CreateProcess(nil, cmdLinePtr, nil, nil, false, CREATE_NO_WINDOW, nil, nil, si, pi)
	syscall.CloseHandle(pi.Process) // Освобождает ресурсы, не дожидаясь завершения
	syscall.CloseHandle(pi.Thread)
}

// countFilesInDir подсчитывает количество файлов в папке рекурсивно
func countFilesInDir(dir string) int {
	count := 0
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}

// copyDirReplace копирует папку из src в dst
func copyDirReplace(src, dst string, replace bool) error {
	// Проверяет, что источник существует и является папкой
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("папка-источник не найдена: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("источник не является папкой: %s", src)
	}

	// Если режим замены - удаляет старую папку целиком
	if replace {
		if _, err := os.Stat(dst); err == nil {
			log.Printf(" Удаление старого содержимого: %s", dst)
			if err := removeDirectoryContents(dst); err != nil {
				return fmt.Errorf("не удалось удалить старую папку %s: %w", dst, err)
			}
		}
	}

	// Создаёт целевую папку
	if err := os.MkdirAll(dst, 0755); err != nil {
		return fmt.Errorf("не удалось создать папку %s: %w", dst, err)
	}

	// Рекурсивно копирует содержимое
	return filepath.Walk(src, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Вычисляет относительный путь
		relPath, err := filepath.Rel(src, srcPath)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			// Создаёт подпапки
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Копирует файл
		if err := copyReplace(srcPath, dstPath); err != nil {
			return fmt.Errorf("ошибка копирования %s -> %s: %w", srcPath, dstPath, err)
		}

		return nil
	})
}

// removeDirectoryContents удаляет папку со всем содержимым, используя надёжный метод для Windows
func removeDirectoryContents(dir string) error {
	// Сначала пробует стандартный метод
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}

	// Если не получилось - удаляет содержимое вручную
	var files []string
	var dirs []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Игнорирует ошибки доступа
		}
		if path == dir {
			return nil
		}
		if info.IsDir() {
			dirs = append(dirs, path)
		} else {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Удаляет все файлы с повторными попытками
	for _, f := range files {
		for range 3 {
			if err := os.Remove(f); err == nil || os.IsNotExist(err) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Удаляет папки в обратном порядке (сначала вложенные)
	for i := len(dirs) - 1; i >= 0; i-- {
		for range 3 {
			if err := os.Remove(dirs[i]); err == nil || os.IsNotExist(err) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Удаляет корневую папку
	for range 5 {
		if err := os.Remove(dir); err == nil || os.IsNotExist(err) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Проверяет, удалилась ли папка
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	return fmt.Errorf("не удалось полностью удалить папку: %s", dir)
}

// formatSize преобразует размер в удобночитаемые величины (Байт, КБ, МБ)
func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d Байт", size)
	}
	fSize := float64(size) / 1024.0
	if fSize < 1024.0 {
		return fmt.Sprintf("%.2f КБ", fSize)
	}
	fSize /= 1024.0
	return fmt.Sprintf("%.2f МБ", fSize)
}
