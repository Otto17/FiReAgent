// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// historyFileName содержит имя файла истории
const historyFileName = "update_history.json"

// Кастомный формат времени: дд.мм.гг(ЧЧ:ММ:СС)
const customTimeLayout = "02.01.06(15:04:05)"

// UpdateEntry описывает одну запись об обновлении
type UpdateEntry struct {
	Version   string `json:"version"`          // Версия
	AppliedAt string `json:"applied_at"`       // Пишет в customTimeLayout
	Source    string `json:"source,omitempty"` // GitHub | GitFlic
}

// UpdateHistory хранит общую историю установленных версий
type UpdateHistory struct {
	Last     string        `json:"last"`
	Versions []UpdateEntry `json:"versions"`
}

// HistoryPath возвращает полный путь к файлу истории
func historyPath(updateDir string) string {
	return filepath.Join(updateDir, historyFileName)
}

// ReadUpdateHistory считывает историю обновлений из файла
func readUpdateHistory(updateDir string) (UpdateHistory, error) {
	p := historyPath(updateDir)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return UpdateHistory{}, nil // Возвращает пустую историю если файл еще не создан
		}
		return UpdateHistory{}, err
	}
	var h UpdateHistory
	if err := json.Unmarshal(b, &h); err != nil {
		return UpdateHistory{}, fmt.Errorf("поврежден JSON %s: %w", p, err)
	}
	return h, nil
}

// WriteUpdateHistory записывает историю обновлений в файл
func writeUpdateHistory(updateDir string, h UpdateHistory) error {
	p := historyPath(updateDir)
	tmp := p + ".tmp"
	data, err := json.MarshalIndent(h, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	_ = os.Remove(p)
	return os.Rename(tmp, p) // Использует временный файл и переименование для атомарной записи
}

// AppendUpdateHistory добавляет новую запись об обновлении в историю и сохраняет ее
func appendUpdateHistory(updateDir, newVersion, source string) error {
	h, _ := readUpdateHistory(updateDir) // Игнорирует ошибку чтения чтобы инициализировать пустую историю
	h.Versions = append(h.Versions, UpdateEntry{
		Version:   newVersion,
		AppliedAt: time.Now().Local().Format(customTimeLayout),
		Source:    canonicalRepoName(source),
	})
	h.Last = newVersion
	return writeUpdateHistory(updateDir, h)
}

// CanonicalRepoName приводит имя репозитория к стандартному формату
func canonicalRepoName(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "github":
		return "GitHub"
	case "gitflic":
		return "GitFlic"
	default:
		return s
	}
}
