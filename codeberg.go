package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Codeberg（Gitea）没有自动找 README 的端点，
// 需要先列根目录，找出 README 的文件名，再取内容。
func fetchCodebergReadme(info *RepoInfo) (string, error) {
	// 第一步：列出根目录文件
	listURL := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s/contents",
		info.Owner, info.Repo)

	resp, err := httpClient.Get(listURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrReadmeNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Codeberg API error: %s", resp.Status)
	}

	var files []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return "", err
	}

	// 第二步：匹配 README（不区分大小写，允许 README.md/.rst/.txt 等）
	var readmePath string
	for _, f := range files {
		if f.Type != "file" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(f.Name), "readme") {
			readmePath = f.Path
			break
		}
	}
	if readmePath == "" {
		return "", ErrReadmeNotFound
	}

	// 第三步：取具体文件内容
	contentURL := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s/contents/%s",
		info.Owner, info.Repo, readmePath)

	req, err := http.NewRequest("GET", contentURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	resp2, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	return decodeReadmeResponse(resp2)
}

func fetchCodebergMeta(info *RepoInfo) (*RepoMeta, error) {
	apiURL := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s",
		info.Owner, info.Repo)

	resp, err := httpClient.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Codeberg API error: %s", resp.Status)
	}

	var raw struct {
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		Stars       int    `json:"stars_count"`
		Language    string `json:"language"`
		HTMLURL     string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	return &RepoMeta{
		FullName:    raw.FullName,
		Description: raw.Description,
		Stars:       raw.Stars,
		Language:    raw.Language,
		URL:         raw.HTMLURL,
	}, nil
}
