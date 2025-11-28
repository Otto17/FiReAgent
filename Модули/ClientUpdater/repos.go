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
	"strings"
	"time"
)

// CheckResult содержит информацию о последнем доступном релизе
type CheckResult struct {
	Repo          string // Название репозитория (gitflic или github)
	RemoteVersion string // Удалённая версия релиза
	AssetName     string // Имя файла ассета
	AssetURL      string // URL для скачивания ассета
	ExpectedSHA   string // Ожидаемый SHA хэш
}

// NeedUpdate проверяет, требуется ли обновление, сравнивая локальную и удаленную версии
func (cr *CheckResult) NeedUpdate(local string) bool {
	const layout = "02.01.06"
	rt, err := time.Parse(layout, cr.RemoteVersion)
	if err != nil {
		return true
	}
	lt, err := time.Parse(layout, local)
	if err != nil {
		return true
	}
	return rt.After(lt)
}

// CheckLatest ищет последний доступный релиз, используя указанный приоритет источников
func CheckLatest(ctx context.Context, primary, ghURL, gfURL, gfToken string) (*CheckResult, []string, error) {
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

	switch p {
	case "github":
		if res, err := checkLatestFromGitHub(ctx, ghURL); err == nil {
			return res, trace, nil
		} else {
			// Использует GitFlic, если GitHub недоступен
			add("Не удалось получить с GitHub", err, "пробуем GitFlic")
			if res2, err2 := checkLatestFromGitFlic(ctx, gfURL, gfToken); err2 == nil {
				return res2, trace, nil
			} else {
				add("Не удалось получить с GitFlic", err2, "")
				return nil, trace, fmt.Errorf("оба источника недоступны")
			}
		}

	case "gitflic":
		if res, err := checkLatestFromGitFlic(ctx, gfURL, gfToken); err == nil {
			return res, trace, nil
		} else {
			// Использует GitHub, если GitFlic недоступен
			add("Не удалось получить с GitFlic", err, "пробуем GitHub")
			if res2, err2 := checkLatestFromGitHub(ctx, ghURL); err2 == nil {
				return res2, trace, nil
			} else {
				add("Не удалось получить с GitHub", err2, "")
				return nil, trace, fmt.Errorf("оба источника недоступны")
			}
		}
	}

	// Выполняется дефолтная логика: GitFlic -> GitHub
	if res, err := checkLatestFromGitFlic(ctx, gfURL, gfToken); err == nil {
		return res, trace, nil
	} else {
		add("Не удалось получить с GitFlic", err, "пробуем GitHub")
		if res2, err2 := checkLatestFromGitHub(ctx, ghURL); err2 == nil {
			return res2, trace, nil
		} else {
			add("Не удалось получить с GitHub", err2, "")
			return nil, trace, fmt.Errorf("оба источника недоступны")
		}
	}
}

// -------------------------------------------------------------
// GitHub
// -------------------------------------------------------------

// ghRelease представляет структуру ответа API GitHub для релиза
type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// ghAsset представляет структуру ответа API GitHub для ассета
type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// toGitHubAPI преобразует пользовательский URL в URL для GitHub API releases/latest
func toGitHubAPI(urlStr string) (string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(u.Host, "api.github.com") && strings.HasPrefix(u.Path, "/repos/") {
		return u.String(), nil
	}
	if strings.EqualFold(u.Host, "github.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 4 && parts[2] == "releases" && parts[3] == "latest" {
			return fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", parts[0], parts[1]), nil
		}
	}
	return "", fmt.Errorf("не удалось преобразовать %q к API releases/latest", urlStr)
}

// checkLatestFromGitHub получает информацию о последнем релизе через GitHub API
func checkLatestFromGitHub(ctx context.Context, ghURL string) (*CheckResult, error) {
	api, err := toGitHubAPI(ghURL)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "FiReAgent-ClientUpdater/1.0")

	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		// Добавление токена для повышения лимита запросов
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}

	// Поиск ассета, соответствующего шаблону
	re := regexp.MustCompile(zipAssetPattern)
	for _, a := range rel.Assets {
		if m := re.FindStringSubmatch(a.Name); m != nil {
			return &CheckResult{
				Repo:          "github",
				RemoteVersion: m[1],
				AssetName:     a.Name,
				AssetURL:      a.BrowserDownloadURL,
				ExpectedSHA:   "",
			}, nil
		}
	}
	return nil, fmt.Errorf("ассет по шаблону не найден: %s", zipAssetPattern)
}

// -------------------------------------------------------------
// GitFlic
// -------------------------------------------------------------

// gfReleases представляет корневой ответ API GitFlic для списка релизов
type gfReleases struct {
	Embedded struct {
		ReleaseTagModelList []gfRelease `json:"releaseTagModelList"`
	} `json:"_embedded"`
}

// gfRelease представляет отдельный релиз GitFlic
type gfRelease struct {
	TagName         string    `json:"tagName"`
	AttachmentFiles []gfAsset `json:"attachmentFiles"`
	PreRelease      bool      `json:"preRelease"`
}

// gfAsset представляет файл-вложение в релизе GitFlic
type gfAsset struct {
	Name       string `json:"name"`
	Link       string `json:"link"`
	HashSha256 string `json:"hashSha256"`
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

// checkLatestFromGitFlic получает информацию о последнем релизе через GitFlic API
func checkLatestFromGitFlic(ctx context.Context, gfURL, token string) (*CheckResult, error) {
	api, err := toGitFlicAPI(gfURL)
	if err != nil {
		return nil, err
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "FiReAgent-ClientUpdater/1.0")
	if strings.TrimSpace(token) != "" {
		// Передача токена авторизации, если он предоставлен
		req.Header.Set("Authorization", "token "+strings.TrimSpace(token))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitFlic API: %w", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitFlic API status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var rels gfReleases
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}
	latest := pickLatestGF(rels.Embedded.ReleaseTagModelList)
	if latest == nil {
		return nil, fmt.Errorf("не найден релиз с валидным tagName")
	}

	// Поиск ассета, соответствующего шаблону, среди вложений
	re := regexp.MustCompile(zipAssetPattern)
	for _, a := range latest.AttachmentFiles {
		if m := re.FindStringSubmatch(a.Name); m != nil {
			return &CheckResult{
				Repo:          "gitflic",
				RemoteVersion: m[1],
				AssetName:     a.Name,
				AssetURL:      a.Link,
				ExpectedSHA:   strings.ToLower(strings.TrimSpace(a.HashSha256)),
			}, nil
		}
	}
	return nil, fmt.Errorf("в релизе нет ассета по шаблону: %s", zipAssetPattern)
}

// pickLatestGF выбирает самый свежий (по дате в теге) релиз, игнорируя PreRelease
func pickLatestGF(list []gfRelease) *gfRelease {
	var best *gfRelease
	var bestT time.Time
	for i := range list {
		r := &list[i]
		if r.PreRelease {
			// Пропускает предварительные релизы
			continue
		}
		t, err := time.Parse("02.01.06", r.TagName)
		if err != nil {
			// Игнорирует теги, которые не соответствуют формату даты
			continue
		}
		if best == nil || t.After(bestT) {
			best = r
			bestT = t
		}
	}
	return best
}
