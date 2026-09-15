package main

import (
	"fmt"
	"net/url"
	"strings"
)

// RepoInfo 是仓库 URL 解析后的统一结构。
type RepoInfo struct {
	Host  string // github.com / codeberg.org / gitee.com
	Owner string
	Repo  string
}

// ParseRepoURL 支持带或不带协议的 URL，也支持末尾带 .git。
func ParseRepoURL(rawURL string) (*RepoInfo, error) {
	// 允许用户省略协议：github.com/foo/bar
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	host := strings.ToLower(u.Host)
	host = strings.Split(host, ":")[0]

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("URL path must contain owner and repo: %s", u.Path)
	}

	repo := strings.TrimSuffix(parts[1], ".git")

	return &RepoInfo{
		Host:  host,
		Owner: parts[0],
		Repo:  repo,
	}, nil
}
