// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// downloadWithChecksum выполняет загрузку файла по URL с проверкой контрольной суммы SHA256
func downloadWithChecksum(src, dest, expectedSHA string, headers map[string]string) error {
	_ = os.Remove(dest) // Удаляет целевой файл перед началом загрузки, если он существует

	req, err := http.NewRequest(http.MethodGet, src, nil)
	if err != nil {
		return err
	}

	pu, _ := url.Parse(src)
	base := ""
	if pu != nil {
		base = pu.Scheme + "://" + pu.Host
	}

	// Добавляет базовый URL к User-Agent для идентификации источника запроса
	req.Header.Set("User-Agent", "FiReAgent-ClientUpdater/1.0 (+ "+base+")")
	for k, v := range headers {
		req.Header.Set(k, v) // Устанавливает дополнительные заголовки запроса
	}

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("скачивание: %w", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("скачивание: статус %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	h := sha256.New()
	// Использует MultiWriter для одновременной записи данных в файл и подсчета хэша
	mw := io.MultiWriter(out, h)
	buf := make([]byte, 256*1024)
	if _, err := io.CopyBuffer(mw, resp.Body, buf); err != nil {
		_ = os.Remove(dest) // Удаляет поврежденный файл, чтобы предотвратить использование неполных данных
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	sum := hex.EncodeToString(h.Sum(nil))

	// Проверяет, совпадает ли полученный хэш с ожидаемой контрольной суммой
	if strings.TrimSpace(expectedSHA) != "" && !strings.EqualFold(sum, expectedSHA) {
		_ = os.Remove(dest) // Удаляет файл, если контрольная сумма не совпала
		return fmt.Errorf("контрольная сумма не совпала (ожидалось %s, получено %s)", expectedSHA, sum)
	}
	return nil
}
