package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func fetchGitHubReadme(info *RepoInfo) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/readme",
		info.Owner, info.Repo)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	return decodeReadmeResponse(resp)
}

func fetchGitHubMeta(info *RepoInfo) (*RepoMeta, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s",
		info.Owner, info.Repo)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error: %s", resp.Status)
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
