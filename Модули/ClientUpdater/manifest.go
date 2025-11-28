// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Action представляет тип операции с файлом
type Action string

const (
	ActUpdate Action = "update" // Операция обновления или добавления файла
	ActDelete Action = "delete" // Операция удаления файла
)

// FileOp описывает операцию над одним файлом в процессе обновления
type FileOp struct {
	Src    string `toml:"Src"`    // Путь к файлу внутри распакованного архива
	Dest   string `toml:"Dest"`   // Путь назначения в базовой директории "C:\Program Files\FiReAgent"
	Action Action `toml:"Action"` // Тип выполняемого действия "update или delete"
}

// Manifest представляет структуру файла манифеста "update.toml"
type Manifest struct {
	Version string   `toml:"version"` // Версия обновления, указанная в манифесте
	Files   []FileOp `toml:"files"`   // Список файловых операций
}

// loadManifest загружает и десериализует файл манифеста TOML
func loadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := toml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("ошибка TOML: %w", err)
	}
	return &m, nil
}

// resolveDest нормализует путь назначения и проверяет, что он находится внутри baseDir
func resolveDest(baseDir, raw string) (string, error) {
	rel := strings.TrimSpace(raw)
	if rel == "" {
		return "", fmt.Errorf("пустой Dest")
	}
	rel = filepath.FromSlash(rel)
	var abs string
	if filepath.IsAbs(rel) {
		abs = filepath.Clean(rel)
	} else {
		abs = filepath.Clean(filepath.Join(baseDir, rel))
	}
	
	// Проверяет, что путь назначения находится строго внутри базовой директории
	cBase := filepath.Clean(baseDir)
	if !strings.EqualFold(cBase, abs) && !strings.HasPrefix(strings.ToLower(abs)+string(os.PathSeparator), strings.ToLower(cBase)+string(os.PathSeparator)) {
		return "", fmt.Errorf("путь вне базовой директории запрещён: %s", abs)
	}
	return abs, nil
}
