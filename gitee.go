package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
)

func fetchGiteeReadme(info *RepoInfo) (string, error) {
	apiURL := fmt.Sprintf("https://gitee.com/api/v5/repos/%s/%s/readme",
		info.Owner, info.Repo)

	// Gitee 推荐用 access_token query 参数认证
	if token := os.Getenv("GITEE_TOKEN"); token != "" {
		apiURL += "?access_token=" + url.QueryEscape(token)
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	return decodeReadmeResponse(resp)
}

func fetchGiteeMeta(info *RepoInfo) (*RepoMeta, error) {
	apiURL := fmt.Sprintf("https://gitee.com/api/v5/repos/%s/%s",
		info.Owner, info.Repo)

	if token := os.Getenv("GITEE_TOKEN"); token != "" {
		apiURL += "?access_token=" + url.QueryEscape(token)
	}

	resp, err := httpClient.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gitee API error: %s", resp.Status)
	}

	var raw struct {
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		Stars       int    `json:"stargazers_count"`
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
