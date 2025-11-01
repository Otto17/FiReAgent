// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ApplyOperations применяет операции обновления и удаления из манифеста
func applyOperations(extractDir, baseDir string, man *Manifest) error {
	fiRoot := filepath.Join(extractDir, "FiReAgent")

	log.Printf("Применение манифеста: %d операций", len(man.Files))
	var updatedCount, deletedCount, skippedDeleteCount int

	for _, it := range man.Files {
		switch it.Action {
		case ActUpdate:
			srcRel := filepath.FromSlash(strings.TrimLeft(it.Src, `/\`))
			if srcRel == "" {
				return fmt.Errorf("files.update: не задан Src")
			}
			srcAbs := filepath.Clean(filepath.Join(fiRoot, srcRel))

			// Проверяет, что источник находится внутри распакованного каталога FiReAgent
			rootClean := filepath.Clean(fiRoot)
			if !strings.EqualFold(rootClean, srcAbs) && !strings.HasPrefix(strings.ToLower(srcAbs)+string(os.PathSeparator), strings.ToLower(rootClean)+string(os.PathSeparator)) {
				return fmt.Errorf("Src вне FiReAgent/: %s", it.Src)
			}

			destAbs, err := resolveDest(baseDir, orDefault(it.Dest, it.Src))
			if err != nil {
				return err
			}

			// Вычисляет относительные пути для записи в лог
			srcLog := filepath.ToSlash(strings.TrimPrefix(srcAbs, fiRoot+string(os.PathSeparator)))
			destLog := destAbs
			if rel, err := filepath.Rel(baseDir, destAbs); err == nil {
				destLog = filepath.ToSlash(rel)
			}

			// Получает размер файла для отображения в логе
			var size int64 = -1
			if inf, err := os.Stat(srcAbs); err == nil {
				size = inf.Size()
			}

			if size >= 0 {
				log.Printf("ОБНОВЛЕНИЕ: %s -> %s (%d байт)", srcLog, destLog, size)
			} else {
				log.Printf("ОБНОВЛЕНИЕ: %s -> %s", srcLog, destLog)
			}

			if err := copyReplace(srcAbs, destAbs); err != nil {
				return fmt.Errorf("ошибка применения обновления: обновлено %s -> %s: %w", srcAbs, destAbs, err)
			}

			updatedCount++
			log.Printf("УСПЕХ: %s", destLog)

			// Небольшая задержка помогает предотвратить блокировки антивирусами или программами индексации
			time.Sleep(20 * time.Millisecond)

		case ActDelete:
			destAbs, err := resolveDest(baseDir, it.Dest)
			if err != nil {
				return err
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
				return fmt.Errorf("ошибка применения обновления: удалён %s: %w", destAbs, err)
			}

			if existed {
				deletedCount++
				log.Printf("УСПЕХ: %s (удалён)", destLog)
			} else {
				skippedDeleteCount++
				log.Printf("УСПЕХ: %s (пропуск)", destLog)
			}

		default:
			return fmt.Errorf("неизвестный Action: %s", it.Action)
		}
	}

	log.Printf("Сводка: обновлено=%d, удалено=%d, пропущено удалений=%d", updatedCount, deletedCount, skippedDeleteCount)
	return nil
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
	for range 5 {
		tmp := dst + ".tmp"
		if err := copyFile(src, tmp, info.Mode()); err != nil {
			return err
		}
		_ = os.Remove(dst)
		if err := os.Rename(tmp, dst); err == nil {
			return nil
		}
		_ = os.Remove(tmp)
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("не удалось заменить файл: %s", dst)
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

// DeletePath удаляет файл или каталог, включая рекурсивное удаление каталогов
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
