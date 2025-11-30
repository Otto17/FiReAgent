// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// CheckResult содержит информацию о релизе
type CheckResult struct {
	Repo          string    // Название репозитория (gitflic или github)
	RemoteVersion string    // Удалённая версия релиза
	AssetName     string    // Имя файла ассета
	AssetURL      string    // URL для скачивания ассета
	ExpectedSHA   string    // Ожидаемый SHA хэш
	ReleaseDate   time.Time // Дата релиза для сортировки
}

// CheckUpdates ищет ВСЕ доступные обновления новее локальной (текущей) версии и возвращает их отсортированными по дате (от старых к новым)
func CheckUpdates(ctx context.Context, localVer, primary, ghURL, gfURL, gfToken string) ([]CheckResult, []string, error) {
	p := strings.ToLower(strings.TrimSpace(primary))
	var trace []string

	// add добавляет сообщение в трассу для отладки
	add := func(msg string, err error, tail string) {
		if err != nil {
			if tail != "" {
				trace = append(trace, fmt.Sprintf("%s: %v — %s", msg, err, tail))
			} else {
				trace = append(trace, fmt.Sprintf("%s: %v", msg, err))
			}
		}
	}

	var results []CheckResult
	var err error

	// Логика выбора репозитория
	switch p {
	case "github":
		results, err = getUpdatesFromGitHub(ctx, ghURL, localVer)
		if err == nil {
			return results, trace, nil
		}
		add("GitHub не ответил или ошибка", err, "пробуем GitFlic")

		results, err = getUpdatesFromGitFlic(ctx, gfURL, gfToken, localVer)
		if err == nil {
			return results, trace, nil
		}
		add("GitFlic не ответил", err, "")

	case "gitflic":
		results, err = getUpdatesFromGitFlic(ctx, gfURL, gfToken, localVer)
		if err == nil {
			return results, trace, nil
		}
		add("GitFlic не ответил или ошибка", err, "пробуем GitHub")

		results, err = getUpdatesFromGitHub(ctx, ghURL, localVer)
		if err == nil {
			return results, trace, nil
		}
		add("GitHub не ответил", err, "")
	}

	// Логика по умолчанию (если основной репозиторий не задан или неизвестен): GitFlic -> GitHub
	results, err = getUpdatesFromGitFlic(ctx, gfURL, gfToken, localVer)
	if err == nil {
		return results, trace, nil
	}
	add("GitFlic (default) не ответил", err, "пробуем GitHub")

	results, err = getUpdatesFromGitHub(ctx, ghURL, localVer)
	if err == nil {
		return results, trace, nil
	}
	add("GitHub не ответил", err, "")

	return nil, trace, fmt.Errorf("не удалось получить список обновлений ни из одного источника")
}

// parseDate парсит версию вида "дд.мм.гг" в time.Time
func parseDate(v string) (time.Time, error) {
	return time.Parse("02.01.06", strings.TrimSpace(v))
}

// sortUpdates сортирует слайс обновлений по возрастанию даты (сначала старые, потом новые)
func sortUpdates(list []CheckResult) {
	sort.Slice(list, func(i, j int) bool {
		return list[i].ReleaseDate.Before(list[j].ReleaseDate)
	})
}

// -------------------------------------------------------------
// GitHub
// -------------------------------------------------------------

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// toGitHubAPIList преобразует URL в URL для GitHub API из releases/latest в /releases (для получения списка)
func toGitHubAPIList(urlStr string) (string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}

	// Если в конфиге указано .../releases/latest, обрезуется в /latest
	u.Path = strings.TrimSuffix(u.Path, "/latest")

	if strings.EqualFold(u.Host, "api.github.com") && strings.HasPrefix(u.Path, "/repos/") {
		return u.String(), nil
	}
	if strings.EqualFold(u.Host, "github.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		// Ожидание получить: owner/repo/releases
		if len(parts) >= 3 && parts[2] == "releases" {
			return fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", parts[0], parts[1]), nil
		}
	}
	return "", fmt.Errorf("не удалось преобразовать URL %q к API списка релизов", urlStr)
}

// getUpdatesFromGitHub запрашивает список релизов с GitHub и отбирает те, что новее локальной (текущей) версии
func getUpdatesFromGitHub(ctx context.Context, ghURL, localVer string) ([]CheckResult, error) {
	// Формирует URL API
	api, err := toGitHubAPIList(ghURL)
	if err != nil {
		return nil, err
	}

	// Создаёт запрос с заголовками
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "FiReAgent-ClientUpdater/1.0")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Выполняет запрос
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	// Декодирует JSON ответ
	var releases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	localTime, _ := parseDate(localVer) // Дата локальной версии
	var updates []CheckResult
	re := regexp.MustCompile(zipAssetPattern)

	// Фильтрует релизы
	for _, r := range releases {
		remoteTime, err := parseDate(r.TagName)
		if err != nil {
			continue // Пропускает теги с неправильным форматом даты
		}

		// Берёт только те версии, дата которых строго больше локальной версии
		if !remoteTime.After(localTime) {
			continue
		}

		// Ищет нужный ZIP-ассет внутри релиза
		for _, a := range r.Assets {
			if m := re.FindStringSubmatch(a.Name); m != nil {
				updates = append(updates, CheckResult{
					Repo:          "github",
					RemoteVersion: m[1],
					AssetName:     a.Name,
					AssetURL:      a.BrowserDownloadURL,
					ExpectedSHA:   "", // GitHub в стандартном JSON не отдает SHA256 ассета
					ReleaseDate:   remoteTime,
				})
				break // Один релиз - один ассет
			}
		}
	}

	if len(updates) == 0 {
		return nil, fmt.Errorf("нет новых версий")
	}

	sortUpdates(updates)
	return updates, nil
}

// -------------------------------------------------------------
// GitFlic
// -------------------------------------------------------------

// gfReleases - корневая структура ответа GitFlic (содержит список тегов)
type gfReleases struct {
	Embedded struct {
		ReleaseTagModelList []gfRelease `json:"releaseTagModelList"`
	} `json:"_embedded"`
}

// gfRelease - отдельный релиз (тег) в GitFlic
type gfRelease struct {
	TagName         string    `json:"tagName"`         // Имя тега (версия)
	AttachmentFiles []gfAsset `json:"attachmentFiles"` // Список вложений (файлов)
	PreRelease      bool      `json:"preRelease"`      // Флаг предварительного релиза
}

// gfAsset - файл-вложение в релизе
type gfAsset struct {
	Name       string `json:"name"`       // Имя файла
	Link       string `json:"link"`       // Ссылка на скачивание
	HashSha256 string `json:"hashSha256"` // Хэш файла (GitFlic предоставляет его)
}

// toGitFlicAPI преобразует пользовательский URL в URL для GitFlic API
func toGitFlicAPI(urlStr string) (string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	if !strings.Contains(strings.ToLower(u.Host), "gitflic.ru") {
		return "", fmt.Errorf("ожидался gitflic.ru: %s", u.Host)
	}
	u.Host = "api.gitflic.ru"
	return u.String(), nil
}

// getUpdatesFromGitFlic запрашивает список релизов с GitFlic и отбирает те, что новее локальной (текущей) версии
func getUpdatesFromGitFlic(ctx context.Context, gfURL, token, localVer string) ([]CheckResult, error) {
	// Формирует URL API
	api, err := toGitFlicAPI(gfURL)
	if err != nil {
		return nil, err
	}

	//  Создаёт запрос с заголовками (включая токен)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "FiReAgent-ClientUpdater/1.0")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "token "+strings.TrimSpace(token))
	}

	// Выполняет запрос
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitFlic API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitFlic API status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	// Декодирует JSON ответ
	var rels gfReleases
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}

	localTime, _ := parseDate(localVer) // Дата локальной версии
	var updates []CheckResult
	re := regexp.MustCompile(zipAssetPattern)

	// Фильтрует релизы
	for _, r := range rels.Embedded.ReleaseTagModelList {
		if r.PreRelease {
			continue // Пропускаем PreRelease
		}
		remoteTime, err := parseDate(r.TagName)
		if err != nil {
			continue // Пропускает теги с неправильным форматом даты
		}

		// Берёт только те версии, дата которых строго больше локальной версии
		if !remoteTime.After(localTime) {
			continue
		}

		// Ищет нужный ZIP-ассет внутри релиза
		for _, a := range r.AttachmentFiles {
			if m := re.FindStringSubmatch(a.Name); m != nil {
				updates = append(updates, CheckResult{
					Repo:          "gitflic",
					RemoteVersion: m[1],
					AssetName:     a.Name,
					AssetURL:      a.Link,
					ExpectedSHA:   strings.ToLower(strings.TrimSpace(a.HashSha256)), // Берёт хэш
					ReleaseDate:   remoteTime,
				})
				break // Один релиз - один ассет
			}
		}
	}

	if len(updates) == 0 {
		return nil, fmt.Errorf("нет новых версий")
	}

	sortUpdates(updates)
	return updates, nil
}
