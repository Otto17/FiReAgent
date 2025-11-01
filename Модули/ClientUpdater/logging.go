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
	"sync"
)

const (
	baseLogName = "log_ClientUpdater.log" // Название лог-файла
	maxLogSize  = 1_000_000               // Максимальный размер лог-файла в байтах для ротации (Установлен 1 Мбайт)
	maxLogFiles = 4                       // Максимальное количество архивных лог-файлов для хранения: основной + _0.._3
)

// Потокобезопасный писатель с ротацией
type rotatingWriter struct {
	path string
	mu   sync.Mutex
}

// Общий writer, чтобы писать «чистые» пустые строки без заголовка логгера
var logMulti io.Writer

// ClientUpdaterLogging инициализирует систему логирования, настраивает вывод в stderr и ротируемый файл
func ClientUpdaterLogging() {
	dir := filepath.Join(exeDir(), "log")
	logPath := filepath.Join(dir, baseLogName)

	if err := ensureLogDir(dir); err != nil {
		log.Printf("Не удалось подготовить каталог логов %s: %v (лог только в stderr)", dir, err)
		return
	}

	w := newRotatingWriter(logPath)
	logMulti = io.MultiWriter(os.Stderr, w)
	log.SetOutput(logMulti)

	// Убирает префикс логгера
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("")

	log.Printf("Лог инициализирован: %s", logPath)
}

// LogBlankLines записывает N пустых строк напрямую через общий писатель
func LogBlankLines(n int) {
	if n <= 0 {
		return
	}
	w := logMulti
	if w == nil {
		// Если логгер не инициализирован, использует фоллбек в stderr
		for range n {
			_, _ = os.Stderr.Write([]byte("\n"))
		}
		return
	}
	for range n {
		// Использует logMulti для записи пустой строки без добавления даты/времени/префикса
		_, _ = w.Write([]byte("\n"))
	}
}

// NewRotatingWriter создаёт новый экземпляр RotatingWriter для указанного пути
func newRotatingWriter(path string) *rotatingWriter {
	return &rotatingWriter{path: path}
}

// Write реализует интерфейс io Writer, выполняет ротацию лога перед записью
func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock() // Гарантирует потокобезопасность операции

	dir := filepath.Dir(w.path)
	_ = ensureLogDir(dir) // Убеждается, что каталог существует перед записью

	if needRotate(w.path) {
		_ = rotateLogs(w.path) // Выполняет ротацию, если текущий файл достиг максимального размера
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Write(p)
}

// EnsureLogDir проверяет существование каталога и создает его при необходимости
func ensureLogDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("пустой каталог логов")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return nil
}

// NeedRotate проверяет, превышает ли размер текущего лог-файла максимальный лимит
func needRotate(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false // Возвращает false, если файл не существует
	}
	return info.Size() >= maxLogSize
}

// RotateLogs выполняет ротацию лог-файлов, сдвигая существующие и удаляя самый старый
func rotateLogs(basePath string) error {
	dir := filepath.Dir(basePath)
	base := filepath.Base(basePath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	// Удаляет самый старый архивный файл
	oldest := filepath.Join(dir, fmt.Sprintf("%s_%d%s", name, maxLogFiles-1, ext))
	_ = os.Remove(oldest)

	// Сдвигает все архивные файлы на одну позицию: _i-1 -> _i
	for i := maxLogFiles - 1; i > 0; i-- {
		src := filepath.Join(dir, fmt.Sprintf("%s_%d%s", name, i-1, ext))
		dst := filepath.Join(dir, fmt.Sprintf("%s_%d%s", name, i, ext))
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}

	// Переименовывает текущий лог-файл в файл архива: 0 -> _0
	cur := basePath
	dst := filepath.Join(dir, fmt.Sprintf("%s_0%s", name, ext))
	if _, err := os.Stat(cur); err == nil {
		_ = os.Rename(cur, dst)
	}
	return nil
}

// WriteToLogFile предоставляет единую точку записи в логгер
func WriteToLogFile(format string, args ...any) {
	log.Printf(format, args...)
}
