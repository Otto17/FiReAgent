// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UpdaterConf содержит параметры конфигурации для процесса обновления
type UpdaterConf struct {
	PrimaryRepo string
	GHURL       string
	GFURL       string
	GFToken     string

	ConfPath  string // ConfPath содержит полный путь к файлу конфигурации
	UpdateDir string // UpdateDir содержит путь к директории обновлений (config/Update)
}

const (
	confFileName     = "ClientUpdater.conf" // Название конфига
	updateSubdirPath = "config\\Update"     // Папка, где будет создаваться/храниться конфиг
)

// exeDir возвращает директорию, в которой находится исполняемый файл
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// updateDir возвращает полный путь к директории, предназначенной для хранения файлов обновлений
func updateDir() string {
	return filepath.Join(exeDir(), updateSubdirPath)
}

// defaultConfPath возвращает полный путь к файлу конфигурации по умолчанию
func defaultConfPath() string {
	return filepath.Join(updateDir(), confFileName)
}

// ensureDir создает директорию, если она не существует
func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// writeDefaultConf записывает стандартное содержимое конфигурации в указанный путь
func writeDefaultConf(path string) error {
	content := `# Выбор основного репозитория: "gitflic" или "github" для обновления FiReAgent (резервный задействуется автоматически при проблемах с основным репозиторием)
Update_PrimaryRepo=gitflic

# Ссылка на последний релиз FiReAgent из GitHub (автоматически преобразуется в API URL)
Update_GitHubReleasesURL=https://github.com/Otto17/FiReAgent/releases/latest

# Ссылка на релизы FiReAgent из GitFlic (автоматически преобразуется в API URL)
Update_GitFlicReleasesURL=https://gitflic.ru/project/otto/fireagent/release

# Публичный токен доступа к GitFlic API для проверки и скачивания обновлений (обязателен для GitFlic)
Update_GitFlicToken=efed450c-d7b2-477e-8f8f-88d2a377b8ca
`
	return os.WriteFile(path, []byte(content), 0644)
}

// trimBOM удаляет метку порядка байтов (BOM) из начала строки
func trimBOM(s string) string {
	return strings.TrimPrefix(s, "\uFEFF")
}

// loadOrCreateConf загружает существующий конфигурационный файл или создает новый с настройками по умолчанию
func loadOrCreateConf() (UpdaterConf, error) {
	dir := updateDir()
	if err := ensureDir(dir); err != nil {
		return UpdaterConf{}, fmt.Errorf("не удалось создать директорию %s: %w", dir, err)
	}
	confPath := defaultConfPath()
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		if err := writeDefaultConf(confPath); err != nil {
			return UpdaterConf{}, fmt.Errorf("не удалось записать конфиг по умолчанию: %w", err)
		}
	}

	conf := UpdaterConf{
		PrimaryRepo: "gitflic",
		GHURL:       "https://github.com/Otto17/FiReAgent/releases/latest",
		GFURL:       "https://gitflic.ru/project/otto/fireagent/release",
		GFToken:     "efed450c-d7b2-477e-8f8f-88d2a377b8ca",
		ConfPath:    confPath,
		UpdateDir:   dir,
	}

	f, err := os.Open(confPath)
	if err != nil {
		return conf, fmt.Errorf("не удалось открыть %s: %w", confPath, err)
	}
	defer f.Close()

	values := map[string]string{}
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			line = trimBOM(line) // Удаляет BOM, так как он может присутствовать только в начале файла
			first = false
		}
		
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Удаляет inline комментарии, следующие за значением
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
			if line == "" {
				continue
			}
		}

		eq := strings.IndexRune(line, '=')
		if eq <= 0 {
			continue
		}

		k := strings.TrimSpace(trimBOM(line[:eq]))
		v := strings.TrimSpace(trimBOM(line[eq+1:]))
		values[k] = v
	}
	if err := sc.Err(); err != nil {
		return conf, fmt.Errorf("ошибка чтения конфига: %w", err)
	}

	// Применяет загруженные значения, если они не пустые
	if v := values["Update_PrimaryRepo"]; v != "" {
		conf.PrimaryRepo = v
	}
	if v := values["Update_GitHubReleasesURL"]; v != "" {
		conf.GHURL = v
	}
	if v := values["Update_GitFlicReleasesURL"]; v != "" {
		conf.GFURL = v
	}
	if v := values["Update_GitFlicToken"]; v != "" {
		conf.GFToken = v
	}

	// Нормализует PrimaryRepo
	conf.PrimaryRepo = strings.ToLower(strings.TrimSpace(conf.PrimaryRepo))
	switch conf.PrimaryRepo {
	case "github", "gitflic":
		// ок
	default:
		// Устанавливает значение по умолчанию, если указано некорректное значение
		conf.PrimaryRepo = "gitflic"
	}

	// Проверяет наличие токена, поскольку он необходим для работы с GitFlic
	if conf.PrimaryRepo == "gitflic" && strings.TrimSpace(conf.GFToken) == "" {
		return conf, fmt.Errorf("Update_PrimaryRepo=gitflic, но Update_GitFlicToken пуст — укажите токен")
	}

	return conf, nil
}
