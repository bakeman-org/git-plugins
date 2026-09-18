package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// ---------- Shared ----------

var ErrReadmeNotFound = errors.New("readme not found")

var httpClient = &http.Client{Timeout: 15 * time.Second}

type RepoMeta struct {
	FullName    string
	Description string
	Stars       int
	Language    string
	URL         string
}

type RepoInfo struct {
	Host     string
	Owner    string
	Repo     string
	Branch   string
	FilePath string
}

func (r *RepoInfo) IsFile() bool { return r.FilePath != "" }

// ---------- URL parsing ----------

func ParseRepoURL(rawURL string) (*RepoInfo, error) {
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
	info := &RepoInfo{
		Host:  host,
		Owner: parts[0],
		Repo:  strings.TrimSuffix(parts[1], ".git"),
	}

	if len(parts) >= 5 {
		switch parts[2] {
		case "blob", "raw":
			info.Branch = parts[3]
			info.FilePath = strings.Join(parts[4:], "/")
		case "src":
			if len(parts) >= 6 && parts[3] == "branch" {
				info.Branch = parts[4]
				info.FilePath = strings.Join(parts[5:], "/")
			}
		}
	}
	return info, nil
}

// ---------- Dispatch ----------

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

func FetchRawFile(info *RepoInfo) (string, error) {
	var rawURL string
	switch info.Host {
	case "github.com":
		rawURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
			info.Owner, info.Repo, info.Branch, info.FilePath)
	case "codeberg.org":
		rawURL = fmt.Sprintf("https://codeberg.org/%s/%s/raw/branch/%s/%s",
			info.Owner, info.Repo, info.Branch, info.FilePath)
	case "gitee.com":
		rawURL = fmt.Sprintf("https://gitee.com/%s/%s/raw/%s/%s",
			info.Owner, info.Repo, info.Branch, info.FilePath)
	default:
		return "", fmt.Errorf("unsupported host: %s", info.Host)
	}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	if info.Host == "github.com" {
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			req.Header.Set("Authorization", "token "+token)
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("file not found: %s@%s", info.FilePath, info.Branch)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

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

// ---------- GitHub ----------

func fetchGitHubReadme(info *RepoInfo) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/readme", info.Owner, info.Repo)
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
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", info.Owner, info.Repo)
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
		FullName: raw.FullName, Description: raw.Description,
		Stars: raw.Stars, Language: raw.Language, URL: raw.HTMLURL,
	}, nil
}

// ---------- Codeberg (Gitea) ----------

func fetchCodebergReadme(info *RepoInfo) (string, error) {
	listURL := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s/contents", info.Owner, info.Repo)
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
	apiURL := fmt.Sprintf("https://codeberg.org/api/v1/repos/%s/%s", info.Owner, info.Repo)
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
		FullName: raw.FullName, Description: raw.Description,
		Stars: raw.Stars, Language: raw.Language, URL: raw.HTMLURL,
	}, nil
}

// ---------- Gitee ----------

func fetchGiteeReadme(info *RepoInfo) (string, error) {
	apiURL := fmt.Sprintf("https://gitee.com/api/v5/repos/%s/%s/readme", info.Owner, info.Repo)
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
	apiURL := fmt.Sprintf("https://gitee.com/api/v5/repos/%s/%s", info.Owner, info.Repo)
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
		FullName: raw.FullName, Description: raw.Description,
		Stars: raw.Stars, Language: raw.Language, URL: raw.HTMLURL,
	}, nil
}

// ---------- Clipboard ----------

func copyToClipboard(text string) error {
	type tool struct {
		name string
		args []string
	}

	var candidates []tool
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		candidates = append(candidates, tool{"wl-copy", nil})
	}
	if os.Getenv("DISPLAY") != "" {
		candidates = append(candidates,
			tool{"xclip", []string{"-selection", "clipboard"}},
			tool{"xsel", []string{"--clipboard", "--input"}},
		)
	}
	candidates = append(candidates,
		tool{"wl-copy", nil},
		tool{"xclip", []string{"-selection", "clipboard"}},
		tool{"pbcopy", nil},
	)

	var lastErr error
	for _, c := range candidates {
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, c.args...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Start(); err != nil {
			lastErr = err
			continue
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("no clipboard tool found (install wl-clipboard / xclip / xsel)")
}

// ---------- Styles ----------

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	descStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Italic(true)
	searchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	boxStyle    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
)

// ---------- Rendering ----------

func RenderMarkdown(md string, width int) (string, error) {
	if width < 20 {
		width = 20
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithWordWrap(width),
	)
	if err != nil {
		r, err = glamour.NewTermRenderer(
			glamour.WithStandardStyle("notty"),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return "", err
		}
	}
	return r.Render(md)
}

func RenderMeta(meta *RepoMeta) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(meta.FullName))
	b.WriteString("\n\n")
	if meta.Description != "" {
		b.WriteString(descStyle.Render(meta.Description))
		b.WriteString("\n\n")
	}
	var badges []string
	if meta.Language != "" {
		badges = append(badges, labelStyle.Render("Language: ")+meta.Language)
	}
	if meta.Stars > 0 {
		badges = append(badges, fmt.Sprintf("%s%d", labelStyle.Render("Stars: "), meta.Stars))
	}
	if len(badges) > 0 {
		b.WriteString(strings.Join(badges, "   "))
		b.WriteString("\n")
	}
	if meta.URL != "" {
		b.WriteString(labelStyle.Render(meta.URL))
		b.WriteString("\n")
	}
	return b.String()
}

// ---------- ANSI-aware search helpers ----------

func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// 高亮样式（16 色，终端兼容性最好）：
//   - 普通匹配：黑字 + 黄底
//   - 当前匹配：白字 + 红底（醒目，一眼能看出 n/N 停在哪）
const (
	hlNormalOn  = "\x1b[30;43m" // black on yellow
	hlCurrentOn = "\x1b[97;41m" // bright white on red
	hlReset     = "\x1b[0m"
)

// highlightMatches 用两种颜色高亮一行里所有匹配项：
//   - 普通匹配 → 黄底黑字
//   - 当前匹配（isCurrent=true 时）→ 红底白字
//
// 保留 glamour 在行内已有的颜色：解析时累积每个 rune 位置生效的 SGR，
// 匹配结束后恢复该位置本应生效的 SGR。
func highlightMatches(line, query string, isCurrent bool) string {
	if query == "" || line == "" {
		return line
	}

	type runeInfo struct {
		r       rune
		bytePos int
		sgr     string
	}
	var infos []runeInfo
	var activeSGR strings.Builder

	i := 0
	for i < len(line) {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && !((line[j] >= 'A' && line[j] <= 'Z') || (line[j] >= 'a' && line[j] <= 'z')) {
				j++
			}
			if j < len(line) {
				j++
			}
			sgr := line[i:j]
			if strings.HasSuffix(sgr, "m") {
				if sgr == "\x1b[0m" || sgr == "\x1b[m" {
					activeSGR.Reset()
				} else {
					activeSGR.WriteString(sgr)
				}
			}
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		infos = append(infos, runeInfo{r: r, bytePos: i, sgr: activeSGR.String()})
		i += size
	}
	if len(infos) == 0 {
		return line
	}

	lp := make([]rune, len(infos))
	for k, info := range infos {
		lp[k] = unicode.ToLower(info.r)
	}
	q := []rune(strings.ToLower(query))
	if len(q) == 0 || len(q) > len(lp) {
		return line
	}

	var matches [][2]int
	for k := 0; k+len(q) <= len(lp); k++ {
		ok := true
		for j := range q {
			if lp[k+j] != q[j] {
				ok = false
				break
			}
		}
		if ok {
			matches = append(matches, [2]int{k, k + len(q)})
			k += len(q) - 1
		}
	}
	if len(matches) == 0 {
		return line
	}

	hlOn := hlNormalOn
	if isCurrent {
		hlOn = hlCurrentOn
	}

	var out strings.Builder
	out.Grow(len(line) + len(matches)*48)

	last := 0
	for _, m := range matches {
		sb := infos[m[0]].bytePos
		var eb int
		if m[1] < len(infos) {
			eb = infos[m[1]].bytePos
		} else {
			eb = len(line)
		}

		out.WriteString(line[last:sb])
		out.WriteString(hlReset)
		out.WriteString(hlOn)
		out.WriteString(stripANSI(line[sb:eb]))
		out.WriteString(hlReset)
		if m[1] < len(infos) {
			out.WriteString(infos[m[1]].sgr)
		}
		last = eb
	}
	out.WriteString(line[last:])
	return out.String()
}

// ---------- TUI messages ----------

type copiedMsg struct {
	lines int
	err   error
}
type clearStatusMsg struct{}

// ---------- TUI viewer ----------

type viewerModel struct {
	title      string
	raw        string
	isMarkdown bool

	cacheValid    bool
	cacheRendered []string // glamour 渲染后的原始行（带 glamour SGR）
	cacheStripped []string // 小写、无 ANSI 的纯文本行，用于搜索

	// 当前实际显示的行（带高亮）和它们对应的 cacheRendered 行号
	display       []string
	displaySource []int

	searchMode bool
	searchBuf  string
	query      string
	filter     bool
	matchLines []int // display 里哪些行是匹配行（用于 n/N 跳转）
	matchIdx   int   // matchLines 里的当前下标

	viewport  viewport.Model
	ready     bool
	wrapWidth int
	pendingG  bool

	status   string
	statusOK bool
}

func (m *viewerModel) Init() tea.Cmd {
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
		m.resize(w, h)
	}
	return nil
}

func (m *viewerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.searchMode {
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch km.String() {
			case "enter":
				m.query = m.searchBuf
				m.searchBuf = ""
				m.searchMode = false
				m.matchIdx = 0
				m.applySearch()
				m.jumpToMatch()
				return m, nil
			case "esc":
				m.searchMode = false
				m.searchBuf = ""
				return m, nil
			case "backspace":
				if len(m.searchBuf) > 0 {
					_, size := utf8.DecodeLastRuneInString(m.searchBuf)
					m.searchBuf = m.searchBuf[:len(m.searchBuf)-size]
				}
				return m, nil
			case "ctrl+u":
				m.searchBuf = ""
				return m, nil
			case "ctrl+c":
				return m, tea.Quit
			default:
				if len(km.Text) > 0 {
					m.searchBuf += km.Text
				}
				return m, nil
			}
		}
		return m, nil
	}

	var cmd tea.Cmd

	switch msg := msg.(type) {
	case copiedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("✗ copy failed: %v", msg.err)
			m.statusOK = false
		} else {
			m.status = fmt.Sprintf("✓ copied %d lines to clipboard", msg.lines)
			m.statusOK = true
		}
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{} })
	case clearStatusMsg:
		m.status = ""
		return m, nil

	case tea.KeyPressMsg:
		key := msg.String()

		if m.pendingG {
			m.pendingG = false
			if key == "g" {
				m.viewport.GotoTop()
				return m, nil
			}
		}

		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.query != "" {
				m.query = ""
				m.filter = false
				m.matchIdx = 0
				m.applySearch()
				return m, nil
			}
			return m, tea.Quit
		case "/":
			m.searchMode = true
			m.searchBuf = ""
			return m, nil
		case "n":
			if len(m.matchLines) > 0 {
				old := m.matchIdx
				m.matchIdx = (m.matchIdx + 1) % len(m.matchLines)
				m.refreshMatch(old, m.matchIdx)
				m.jumpToMatch()
			}
			return m, nil
		case "N":
			if len(m.matchLines) > 0 {
				old := m.matchIdx
				m.matchIdx = (m.matchIdx - 1 + len(m.matchLines)) % len(m.matchLines)
				m.refreshMatch(old, m.matchIdx)
				m.jumpToMatch()
			}
			return m, nil
		case "f":
			if m.query != "" {
				m.filter = !m.filter
				m.matchIdx = 0
				m.applySearch()
				m.jumpToMatch()
			}
			return m, nil
		case "c":
			content := m.raw
			lines := strings.Count(content, "\n") + 1
			return m, func() tea.Msg {
				if err := copyToClipboard(content); err != nil {
					return copiedMsg{err: err}
				}
				return copiedMsg{lines: lines}
			}
		case "g":
			m.pendingG = true
			return m, nil
		case "G":
			m.viewport.GotoBottom()
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	}

	if m.ready {
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m *viewerModel) resize(width, height int) {
	vpWidth := width - 4
	vpHeight := height - 4
	if vpWidth < 20 {
		vpWidth = 20
	}
	if vpHeight < 3 {
		vpHeight = 3
	}

	if !m.ready {
		m.viewport = viewport.New(viewport.WithWidth(vpWidth), viewport.WithHeight(vpHeight))
		m.ready = true
	} else {
		m.viewport.SetWidth(vpWidth)
		m.viewport.SetHeight(vpHeight)
	}

	if vpWidth != m.wrapWidth {
		m.wrapWidth = vpWidth
		m.cacheValid = false
		m.applySearch()
	}
}

// applySearch 全量重建 display。使用缓存做匹配/过滤/高亮，不重新渲染 markdown。
func (m *viewerModel) applySearch() {
	if !m.ready || m.wrapWidth == 0 {
		return
	}

	// 1) 惰性重建渲染缓存
	if !m.cacheValid {
		var rendered string
		if m.isMarkdown {
			out, err := RenderMarkdown(m.raw, m.wrapWidth)
			if err != nil {
				rendered = m.raw
			} else {
				rendered = out
			}
		} else {
			rendered = m.raw
		}
		m.cacheRendered = strings.Split(rendered, "\n")
		m.cacheStripped = make([]string, len(m.cacheRendered))
		for i, ln := range m.cacheRendered {
			m.cacheStripped[i] = strings.ToLower(stripANSI(ln))
		}
		m.cacheValid = true
	}

	// 2) 找匹配行
	queryLower := strings.ToLower(m.query)
	var rawMatches []int
	if queryLower != "" {
		for i, s := range m.cacheStripped {
			if strings.Contains(s, queryLower) {
				rawMatches = append(rawMatches, i)
			}
		}
	}

	// 3) 确定 matchLines 的形状（filter 模式和非 filter 模式不同）
	if m.filter && queryLower != "" {
		m.matchLines = make([]int, len(rawMatches))
		for i := range rawMatches {
			m.matchLines[i] = i
		}
	} else {
		m.matchLines = rawMatches
	}

	// matchIdx 越界则归零
	if m.matchIdx >= len(m.matchLines) {
		m.matchIdx = 0
	}

	// 4) 构建 display
	if m.filter && queryLower != "" {
		m.display = make([]string, len(rawMatches))
		m.displaySource = make([]int, len(rawMatches))
		for i, srcIdx := range rawMatches {
			isCurrent := i == m.matchIdx
			m.display[i] = highlightMatches(m.cacheRendered[srcIdx], m.query, isCurrent)
			m.displaySource[i] = srcIdx
		}
	} else {
		m.display = make([]string, len(m.cacheRendered))
		copy(m.display, m.cacheRendered)
		m.displaySource = make([]int, len(m.cacheRendered))
		for i := range m.cacheRendered {
			m.displaySource[i] = i
		}
		// 只重渲染匹配行
		for i, srcIdx := range rawMatches {
			isCurrent := i == m.matchIdx
			m.display[srcIdx] = highlightMatches(m.cacheRendered[srcIdx], m.query, isCurrent)
		}
	}

	m.viewport.SetContent(strings.Join(m.display, "\n"))
}

// refreshMatch 只重渲染受影响的两行（旧的当前匹配恢复普通高亮，新的当前匹配应用当前高亮）。
// n/N 走这条快路径，避免整页重建。
func (m *viewerModel) refreshMatch(oldIdx, newIdx int) {
	if len(m.matchLines) == 0 || len(m.display) == 0 {
		return
	}
	if oldIdx == newIdx {
		return
	}

	// 旧的当前行 → 恢复普通高亮
	if oldIdx >= 0 && oldIdx < len(m.matchLines) {
		dl := m.matchLines[oldIdx]
		if dl >= 0 && dl < len(m.display) {
			src := m.displaySource[dl]
			m.display[dl] = highlightMatches(m.cacheRendered[src], m.query, false)
		}
	}

	// 新的当前行 → 应用当前高亮
	if newIdx >= 0 && newIdx < len(m.matchLines) {
		dl := m.matchLines[newIdx]
		if dl >= 0 && dl < len(m.display) {
			src := m.displaySource[dl]
			m.display[dl] = highlightMatches(m.cacheRendered[src], m.query, true)
		}
	}

	m.viewport.SetContent(strings.Join(m.display, "\n"))
}

func (m *viewerModel) jumpToMatch() {
	if len(m.matchLines) == 0 {
		return
	}
	line := m.matchLines[m.matchIdx]
	off := line - m.viewport.Height()/2
	if off < 0 {
		off = 0
	}
	m.viewport.SetYOffset(off)
}

func (m *viewerModel) View() tea.View {
	if !m.ready {
		return tea.NewView("Loading…")
	}

	title := titleStyle.Render(m.title)
	body := boxStyle.Width(m.viewport.Width()).Render(m.viewport.View())

	var footer string
	switch {
	case m.searchMode:
		footer = searchStyle.Render("/") + m.searchBuf + searchStyle.Render("▌")
	case m.status != "":
		if m.statusOK {
			footer = statusStyle.Render(m.status)
		} else {
			footer = errStyle.Render(m.status)
		}
	case m.query != "":
		pct := int(m.viewport.ScrollPercent() * 100)
		pos := "0/0"
		if len(m.matchLines) > 0 {
			pos = fmt.Sprintf("%d/%d", m.matchIdx+1, len(m.matchLines))
		}
		mode := "all"
		if m.filter {
			mode = "filtered"
		}
		footer = labelStyle.Render(fmt.Sprintf(
			"/%s  [%s]  n/N match %s  f filter  c copy  esc clear  q quit  %d%%",
			m.query, mode, pos, pct))
	default:
		pct := int(m.viewport.ScrollPercent() * 100)
		footer = labelStyle.Render(fmt.Sprintf(
			"q quit · j/k scroll · / search · c copy · %d%%", pct))
	}

	content := strings.Join([]string{title, body, footer}, "\n")

	v := tea.NewView(content)
	v.AltScreen = false
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func runViewer(title, raw string, isMarkdown bool) error {
	m := &viewerModel{title: title, raw: raw, isMarkdown: isMarkdown}
	p := tea.NewProgram(m)
	_, err := p.Run()
	fmt.Println()
	return err
}

// ---------- main ----------

func main() {
	descOnly := flag.Bool("desc", false, "只显示仓库描述，不拉取 README")
	rawOnly := flag.Bool("raw", false, "输出原始内容，不做终端渲染")
	grep := flag.String("grep", "", "非 TTY 输出时只保留包含该字符串的行")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: git readme [--desc] [--raw] [--grep PATTERN] <url>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  git readme https://github.com/aidevjoe/Flip-Clock")
		fmt.Fprintln(os.Stderr, "  git readme https://github.com/vapor/vapor/blob/main/Package.swift")
		fmt.Fprintln(os.Stderr, "  git readme --desc https://codeberg.org/forgejo/forgejo")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "TUI keys:")
		fmt.Fprintln(os.Stderr, "  j/k ↑/↓     scroll      ctrl+d/u   half-page")
		fmt.Fprintln(os.Stderr, "  gg/G        top/bottom  /          search")
		fmt.Fprintln(os.Stderr, "  n/N         next/prev match   f  toggle filter")
		fmt.Fprintln(os.Stderr, "  c           copy raw content to clipboard")
		fmt.Fprintln(os.Stderr, "  esc         clear search (or quit if empty)")
		fmt.Fprintln(os.Stderr, "  q           quit")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Search highlight:")
		fmt.Fprintln(os.Stderr, "  yellow background  → all matches")
		fmt.Fprintln(os.Stderr, "  red background     → current match (n/N position)")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Environment:")
		fmt.Fprintln(os.Stderr, "  GITHUB_TOKEN  GitHub API token")
		fmt.Fprintln(os.Stderr, "  GITEE_TOKEN   Gitee API token")
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	info, err := ParseRepoURL(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing URL: %v\n", err)
		os.Exit(1)
	}

	// ---- 文件模式 ----
	if info.IsFile() {
		content, err := FetchRawFile(info)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching file: %v\n", err)
			os.Exit(1)
		}
		title := fmt.Sprintf("%s/%s:%s", info.Owner, info.Repo, info.FilePath)

		if *rawOnly || !term.IsTerminal(int(os.Stdout.Fd())) {
			if *grep != "" {
				q := strings.ToLower(*grep)
				for _, ln := range strings.Split(content, "\n") {
					if strings.Contains(strings.ToLower(ln), q) {
						fmt.Println(ln)
					}
				}
				return
			}
			fmt.Print(content)
			if !strings.HasSuffix(content, "\n") {
				fmt.Println()
			}
			return
		}

		if err := runViewer(title, content, false); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// ---- 仓库模式 ----
	if *descOnly {
		meta, err := FetchMeta(info)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(RenderMeta(meta))
		return
	}

	readme, err := FetchReadme(info)

	switch {
	case err == nil:
		if *rawOnly {
			if *grep != "" {
				q := strings.ToLower(*grep)
				for _, ln := range strings.Split(readme, "\n") {
					if strings.Contains(strings.ToLower(ln), q) {
						fmt.Println(ln)
					}
				}
				return
			}
			fmt.Print(readme)
			return
		}

		title := info.Owner + "/" + info.Repo

		if term.IsTerminal(int(os.Stdout.Fd())) {
			if err := runViewer(title, readme, true); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		out, rErr := RenderMarkdown(readme, 80)
		if rErr != nil {
			fmt.Fprintf(os.Stderr, "Error rendering README: %v\n", rErr)
			os.Exit(1)
		}
		if *grep != "" {
			q := strings.ToLower(*grep)
			var kept []string
			for _, ln := range strings.Split(out, "\n") {
				if strings.Contains(strings.ToLower(stripANSI(ln)), q) {
					kept = append(kept, ln)
				}
			}
			fmt.Print(strings.Join(kept, "\n"))
			if len(kept) > 0 {
				fmt.Println()
			}
			return
		}
		fmt.Print(out)

	case errors.Is(err, ErrReadmeNotFound):
		meta, mErr := FetchMeta(info)
		if mErr != nil {
			fmt.Fprintf(os.Stderr, "No README found, and failed to fetch repo info: %v\n", mErr)
			os.Exit(1)
		}
		if meta.Description == "" {
			fmt.Fprintf(os.Stderr, "%s has no README and no description.\n", meta.FullName)
			os.Exit(2)
		}
		fmt.Print(RenderMeta(meta))

	default:
		fmt.Fprintf(os.Stderr, "Error fetching README: %v\n", err)
		os.Exit(1)
	}
}
