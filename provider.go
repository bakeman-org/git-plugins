package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrReadmeNotFound 表示目标仓库没有 README。
// 只有这个哨兵错误才会触发回退到仓库描述的逻辑；
// 网络错误、认证错误等不应该静默回退。
var ErrReadmeNotFound = errors.New("readme not found")

// httpClient 带超时，避免网络请求卡死。
var httpClient = &http.Client{Timeout: 15 * time.Second}

// RepoMeta 是各平台仓库元信息的统一表示。
type RepoMeta struct {
	FullName    string // owner/repo
	Description string
	Stars       int
	Language    string
	URL         string
}

// FetchReadme 根据 host 分发到各平台实现。
func FetchReadme(info *RepoInfo) (string, error) {
	switch info.Host {
	case "github.com":
		return fetchGitHubReadme(info)
	case "codeberg.org":
		return fetchCodebergReadme(info)
	case "gitee.com":
		return fetchGiteeReadme(info)
	default:
		return "", fmt.Errorf("unsupported host: %s", info.Host)
	}
}

// FetchMeta 根据 host 分发到各平台实现。
func FetchMeta(info *RepoInfo) (*RepoMeta, error) {
	switch info.Host {
	case "github.com":
		return fetchGitHubMeta(info)
	case "codeberg.org":
		return fetchCodebergMeta(info)
	case "gitee.com":
		return fetchGiteeMeta(info)
	default:
		return nil, fmt.Errorf("unsupported host: %s", info.Host)
	}
}

// decodeReadmeResponse 统一处理 README 响应：
//   - 404        → ErrReadmeNotFound
//   - 其他非200  → 普通错误
//   - 200        → 解析 JSON 并做 Base64 解码
func decodeReadmeResponse(resp *http.Response) (string, error) {
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrReadmeNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %s", resp.Status)
	}

	var result struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Encoding == "" || result.Encoding == "base64" {
		// 清理换行符，Base64 解码对换行敏感
		clean := strings.NewReplacer("\n", "", "\r", "").Replace(result.Content)
		decoded, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(clean)
			if err != nil {
				return "", fmt.Errorf("base64 decode failed: %w", err)
			}
		}
		return string(decoded), nil
	}
	return result.Content, nil
}
