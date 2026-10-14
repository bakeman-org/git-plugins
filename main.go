package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
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

const defaultOwner = "bakeman-org"

func gitConfigGet(key string) (string, error) {
	out, err := exec.Command("git", "config", "--get", key).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitConfigSet(key, value string) error {
	return exec.Command("git", "config", "--global", key, value).Run()
}

func resolveOwner() string {
	if v, err := gitConfigGet("seemystared.user"); err == nil && v != "" {
		return v
	}
	_ = gitConfigSet("seemystared.user", defaultOwner)
	return defaultOwner
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

var ErrReadmeNotFound = errors.New("readme not found")

type StarredRepo struct {
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	HTMLURL     string `json:"html_url"`
	Language    string `json:"language"`
	Stars       int    `json:"stargazers_count"`
}

func githubRequest(method, apiURL string) (*http.Request, error) {
	req, err := http.NewRequest(method, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "git-seemystared/1.0")
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func classifyGitHubError(status, remaining string) string {
	switch {
	case strings.HasPrefix(status, "403") && remaining == "0":
		return "GitHub API rate limit exceeded — set GITHUB_TOKEN to raise it (60/hr anonymous → 5000/hr with token)"
	case strings.HasPrefix(status, "403"):
		return "GitHub API: forbidden (" + status + ")"
	case strings.HasPrefix(status, "401"):
		return "GitHub API: invalid GITHUB_TOKEN"
	default:
		return "GitHub API: " + status
	}
}

func fetchStarred(owner string) ([]StarredRepo, error) {
	var all []StarredRepo
	for page := 1; page <= 50; page++ {
		apiURL := fmt.Sprintf(
			"https://api.github.com/users/%s/starred?per_page=100&page=%d&sort=created&direction=desc",
			owner, page)
		req, err := githubRequest("GET", apiURL)
		if err != nil {
			return nil, err
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusNotFound:
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub user %q not found", owner)
		default:
			remaining := resp.Header.Get("X-RateLimit-Remaining")
			resp.Body.Close()
			return nil, errors.New(classifyGitHubError(resp.Status, remaining))
		}
		var batch []StarredRepo
		err = json.NewDecoder(resp.Body).Decode(&batch)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

func fetchReadme(fullName string) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/readme", fullName)
	req, err := githubRequest("GET", apiURL)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrReadmeNotFound
	}
	if resp.StatusCode != http.StatusOK {
		remaining := resp.Header.Get("X-RateLimit-Remaining")
		return "", errors.New(classifyGitHubError(resp.Status, remaining))
	}
	var result struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
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

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	searchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	boxStyle    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	listItemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	listCursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	listMatchStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	listBarStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	listStarStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("222"))
	listLangStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("109"))
	listDotStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	listDescStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
)

func RenderMarkdown(md string, width int) (string, error) {
	if width < 20 {
		width = 20
	}
	r, err := glamour.NewTermRenderer(glamour.WithWordWrap(width))
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

const (
	hlNormalOn  = "\x1b[30;43m"
	hlCurrentOn = "\x1b[97;41m"
	hlReset     = "\x1b[0m"
)

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

type clearStatusMsg struct{}

type starListModel struct {
	owner string
	repos []StarredRepo

	visible []int
	cursor  int

	searchMode bool
	searchBuf  string
	query      string
	filterMode bool

	showDesc bool

	viewport      viewport.Model
	ready         bool
	width, height int
	pendingG      bool

	rowLine []int

	status   string
	statusOK bool

	chosen  *StarredRepo
	loading bool
}

func (m *starListModel) Init() tea.Cmd {
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
		m.resize(w, h)
	}
	return nil
}

func (m *starListModel) resize(width, height int) {
	m.width, m.height = width, height
	vpW := width - 6
	vpH := height - 6
	if vpW < 20 {
		vpW = 20
	}
	if vpH < 3 {
		vpH = 3
	}
	if !m.ready {
		m.viewport = viewport.New(viewport.WithWidth(vpW), viewport.WithHeight(vpH))
		m.ready = true
	} else {
		m.viewport.SetWidth(vpW)
		m.viewport.SetHeight(vpH)
	}
	m.refresh()
}

func repoMatches(r StarredRepo, q string) bool {
	if q == "" {
		return false
	}
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(r.FullName), q) {
		return true
	}
	if strings.Contains(strings.ToLower(r.Description), q) {
		return true
	}
	if strings.Contains(strings.ToLower(r.Language), q) {
		return true
	}
	return false
}

func (m *starListModel) applySearch() {
	m.visible = m.visible[:0]
	if m.filterMode && m.query != "" {
		for i, r := range m.repos {
			if repoMatches(r, m.query) {
				m.visible = append(m.visible, i)
			}
		}
	} else {
		for i := range m.repos {
			m.visible = append(m.visible, i)
		}
	}
	if m.cursor >= len(m.visible) {
		m.cursor = 0
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.refresh()
}

func (m *starListModel) refresh() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(strings.Join(m.buildLines(), "\n"))
	m.ensureVisible()
}

func (m *starListModel) buildLines() []string {
	width := m.viewport.Width()
	if width < 20 {
		width = 20
	}
	lines := make([]string, 0, len(m.visible)*2)
	m.rowLine = m.rowLine[:0]

	const barW = 2

	for vi, ri := range m.visible {
		r := m.repos[ri]
		m.rowLine = append(m.rowLine, len(lines))

		isCursor := vi == m.cursor
		matches := m.query != "" && repoMatches(r, m.query)

		idxStr := fmt.Sprintf("%3d. ", vi+1)

		var metaParts []string
		metaLen := 0
		if r.Stars > 0 {
			s := fmt.Sprintf("★ %d", r.Stars)
			metaParts = append(metaParts, listStarStyle.Render(s))
			metaLen += utf8.RuneCountInString(s)
		}
		if r.Language != "" {
			if len(metaParts) > 0 {
				metaParts = append(metaParts, listDotStyle.Render(" · "))
				metaLen += 3
			}
			metaParts = append(metaParts, listLangStyle.Render(r.Language))
			metaLen += utf8.RuneCountInString(r.Language)
		}
		metaStr := ""
		if len(metaParts) > 0 {
			metaStr = "  " + strings.Join(metaParts, "")
			metaLen += 2
		}

		nameMax := width - barW - utf8.RuneCountInString(idxStr) - metaLen
		if nameMax < 8 {
			nameMax = 8
		}
		name := r.FullName
		if nr := []rune(name); len(nr) > nameMax {
			name = string(nr[:nameMax-1]) + "…"
		}

		var nameRendered string
		switch {
		case isCursor:
			nameRendered = listCursorStyle.Render(idxStr + name)
		case matches:
			nameRendered = listMatchStyle.Render(idxStr + name)
		default:
			nameRendered = listItemStyle.Render(idxStr + name)
		}

		bar := "  "
		if isCursor {
			bar = listBarStyle.Render("▌ ")
		}

		lines = append(lines, bar+nameRendered+metaStr)

		if m.showDesc && r.Description != "" {
			desc := strings.ReplaceAll(r.Description, "\n", " ")
			desc = strings.TrimSpace(desc)
			indent := strings.Repeat(" ", barW+4)
			maxDesc := width - barW - 4
			if maxDesc < 10 {
				maxDesc = 10
			}
			if dr := []rune(desc); len(dr) > maxDesc {
				desc = string(dr[:maxDesc-1]) + "…"
			}
			lines = append(lines, indent+listDescStyle.Render(desc))
		}
	}
	return lines
}

func (m *starListModel) ensureVisible() {
	if !m.ready || len(m.rowLine) == 0 {
		return
	}
	if m.cursor >= len(m.rowLine) {
		return
	}
	line := m.rowLine[m.cursor]
	h := m.viewport.Height()
	top := m.viewport.YOffset()
	if line < top {
		m.viewport.SetYOffset(line)
	} else if line >= top+h {
		m.viewport.SetYOffset(line - h + 1)
	}
}

func (m *starListModel) moveCursorTo(forward bool) {
	if m.query == "" || len(m.visible) == 0 {
		return
	}
	n := len(m.visible)
	for step := 1; step <= n; step++ {
		var idx int
		if forward {
			idx = (m.cursor + step) % n
		} else {
			idx = (m.cursor - step + n) % n
		}
		if repoMatches(m.repos[m.visible[idx]], m.query) {
			m.cursor = idx
			m.refresh()
			return
		}
	}
}

func (m *starListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.searchMode {
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch km.String() {
			case "enter":
				m.query = m.searchBuf
				m.searchBuf = ""
				m.searchMode = false
				m.cursor = 0
				m.applySearch()
				if m.query != "" {
					for i, ri := range m.visible {
						if repoMatches(m.repos[ri], m.query) {
							m.cursor = i
							m.refresh()
							break
						}
					}
					cnt := 0
					for _, r := range m.repos {
						if repoMatches(r, m.query) {
							cnt++
						}
					}
					m.status = fmt.Sprintf("%d match(es)", cnt)
					m.statusOK = true
					return m, tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg {
						return clearStatusMsg{}
					})
				}
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

	switch msg := msg.(type) {
	case clearStatusMsg:
		m.status = ""
		return m, nil
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyPressMsg:
		if m.loading {
			return m, nil
		}
		key := msg.String()

		if m.pendingG {
			m.pendingG = false
			if key == "g" {
				m.cursor = 0
				m.refresh()
				return m, nil
			}
		}

		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.query != "" {
				m.query = ""
				m.filterMode = false
				m.cursor = 0
				m.applySearch()
				return m, nil
			}
			return m, tea.Quit
		case "j", "down":
			if m.cursor < len(m.visible)-1 {
				m.cursor++
				m.refresh()
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
				m.refresh()
			}
		case "g":
			m.pendingG = true
		case "G":
			if len(m.visible) > 0 {
				m.cursor = len(m.visible) - 1
				m.refresh()
			}
		case "ctrl+d", "pgdown":
			if len(m.visible) == 0 {
				break
			}
			m.cursor += m.viewport.Height() / 2
			if m.cursor >= len(m.visible) {
				m.cursor = len(m.visible) - 1
			}
			m.refresh()
		case "ctrl+u", "pgup":
			m.cursor -= m.viewport.Height() / 2
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.refresh()
		case "/":
			m.searchMode = true
			m.searchBuf = ""
			return m, nil
		case "n":
			m.moveCursorTo(true)
		case "N":
			m.moveCursorTo(false)
		case "f":
			if m.query != "" {
				m.filterMode = !m.filterMode
				m.cursor = 0
				m.applySearch()
				if m.filterMode {
					m.status = "filter: on"
				} else {
					m.status = "filter: off"
				}
				m.statusOK = true
				return m, tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg {
					return clearStatusMsg{}
				})
			}
		case "d":
			m.showDesc = !m.showDesc
			if m.showDesc {
				m.status = "descriptions: on"
			} else {
				m.status = "descriptions: off"
			}
			m.statusOK = true
			m.refresh()
			return m, tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg {
				return clearStatusMsg{}
			})
		case "enter":
			if len(m.visible) > 0 {
				r := m.repos[m.visible[m.cursor]]
				m.chosen = &r
			}
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *starListModel) View() tea.View {
	if !m.ready {
		return tea.NewView("Loading…")
	}

	title := titleStyle.Render(fmt.Sprintf("★  %s's starred repos (%d)", m.owner, len(m.repos)))

	var body string
	if len(m.visible) == 0 {
		var msg string
		if m.query != "" {
			msg = fmt.Sprintf("No matches for %q.", m.query)
		} else {
			msg = "No starred repos."
		}
		body = boxStyle.Width(m.viewport.Width()).Render(labelStyle.Render(msg))
	} else {
		body = boxStyle.Width(m.viewport.Width()).Render(m.viewport.View())
	}

	var footer string
	switch {
	case m.searchMode:
		preview := ""
		if m.searchBuf != "" {
			cnt := 0
			for _, r := range m.repos {
				if repoMatches(r, m.searchBuf) {
					cnt++
				}
			}
			preview = labelStyle.Render(fmt.Sprintf("  (%d)", cnt))
		}
		footer = searchStyle.Render("/") + m.searchBuf + searchStyle.Render("▌") + preview
	case m.loading:
		footer = statusStyle.Render("Loading README…")
	case m.status != "":
		if m.statusOK {
			footer = statusStyle.Render(m.status)
		} else {
			footer = errStyle.Render(m.status)
		}
	case m.query != "":
		mode := "all"
		if m.filterMode {
			mode = "filtered"
		}
		footer = labelStyle.Render(fmt.Sprintf(
			"/%s  [%s]  %d/%d  ·  n/N next/prev  f filter  d desc  esc clear  enter open  q quit",
			m.query, mode, m.cursor+1, len(m.visible)))
	default:
		footer = labelStyle.Render(
			"j/k move · gg/G top/bottom · / search · d desc · enter open README · q quit")
	}

	content := strings.Join([]string{title, body, footer}, "\n")
	v := tea.NewView(content)
	v.AltScreen = false
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

type copiedMsg struct {
	lines int
	err   error
}

type backToParentMsg struct{}

type viewerModel struct {
	title      string
	raw        string
	isMarkdown bool

	cacheValid    bool
	cacheRendered []string
	cacheStripped []string

	display       []string
	displaySource []int

	searchMode bool
	searchBuf  string
	query      string
	filter     bool
	matchLines []int
	matchIdx   int

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
		case "ctrl+c":
			return m, tea.Quit
		case "q", "esc":
			if m.query != "" {
				m.query = ""
				m.filter = false
				m.matchIdx = 0
				m.applySearch()
				return m, nil
			}
			return m, func() tea.Msg { return backToParentMsg{} }
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

func (m *viewerModel) applySearch() {
	if !m.ready || m.wrapWidth == 0 {
		return
	}
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

	queryLower := strings.ToLower(m.query)
	var rawMatches []int
	if queryLower != "" {
		for i, s := range m.cacheStripped {
			if strings.Contains(s, queryLower) {
				rawMatches = append(rawMatches, i)
			}
		}
	}

	if m.filter && queryLower != "" {
		m.matchLines = make([]int, len(rawMatches))
		for i := range rawMatches {
			m.matchLines[i] = i
		}
	} else {
		m.matchLines = rawMatches
	}
	if m.matchIdx >= len(m.matchLines) {
		m.matchIdx = 0
	}

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
		for i, srcIdx := range rawMatches {
			isCurrent := i == m.matchIdx
			m.display[srcIdx] = highlightMatches(m.cacheRendered[srcIdx], m.query, isCurrent)
		}
	}
	m.viewport.SetContent(strings.Join(m.display, "\n"))
}

func (m *viewerModel) refreshMatch(oldIdx, newIdx int) {
	if len(m.matchLines) == 0 || len(m.display) == 0 || oldIdx == newIdx {
		return
	}
	if oldIdx >= 0 && oldIdx < len(m.matchLines) {
		dl := m.matchLines[oldIdx]
		if dl >= 0 && dl < len(m.display) {
			src := m.displaySource[dl]
			m.display[dl] = highlightMatches(m.cacheRendered[src], m.query, false)
		}
	}
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
			"/%s  [%s]  n/N match %s  f filter  c copy  esc/q back  %d%%",
			m.query, mode, pos, pct))
	default:
		pct := int(m.viewport.ScrollPercent() * 100)
		footer = labelStyle.Render(fmt.Sprintf(
			"esc/q back to list · j/k scroll · / search · c copy · %d%%", pct))
	}

	content := strings.Join([]string{title, body, footer}, "\n")
	v := tea.NewView(content)
	v.AltScreen = false
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

type readmeLoadedMsg struct {
	title string
	raw   string
	err   error
}

type appMode int

const (
	modeList appMode = iota
	modeViewer
)

type appModel struct {
	owner string
	repos []StarredRepo

	mode appMode

	list   *starListModel
	viewer *viewerModel

	width, height int
}

func (m *appModel) Init() tea.Cmd {
	return m.list.Init()
}

func fetchReadmeCmd(repo StarredRepo) tea.Cmd {
	return func() tea.Msg {
		readme, err := fetchReadme(repo.FullName)
		if err != nil {
			return readmeLoadedMsg{title: repo.FullName, err: err}
		}
		return readmeLoadedMsg{title: repo.FullName, raw: readme}
	}
}

func (m *appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		var cmds []tea.Cmd
		_, c1 := m.list.Update(msg)
		cmds = append(cmds, c1)
		if m.viewer != nil {
			_, c2 := m.viewer.Update(msg)
			cmds = append(cmds, c2)
		}
		return m, tea.Batch(cmds...)

	case readmeLoadedMsg:
		m.list.loading = false
		if msg.err != nil {
			if errors.Is(msg.err, ErrReadmeNotFound) {
				m.list.status = fmt.Sprintf("✗ %s has no README", msg.title)
			} else {
				m.list.status = fmt.Sprintf("✗ %v", msg.err)
			}
			m.list.statusOK = false
			return m, tea.Tick(4*time.Second, func(time.Time) tea.Msg {
				return clearStatusMsg{}
			})
		}
		m.viewer = &viewerModel{
			title:      msg.title,
			raw:        msg.raw,
			isMarkdown: true,
		}
		if m.width > 0 && m.height > 0 {
			m.viewer.resize(m.width, m.height)
		}
		m.mode = modeViewer
		return m, nil

	case backToParentMsg:
		m.mode = modeList
		m.list.refresh()
		return m, nil
	}

	if m.mode == modeList {
		newList, cmd := m.list.Update(msg)
		if l, ok := newList.(*starListModel); ok {
			m.list = l
		}
		if m.list.chosen != nil {
			repo := *m.list.chosen
			m.list.chosen = nil
			m.list.loading = true
			m.list.refresh()
			return m, tea.Batch(cmd, fetchReadmeCmd(repo))
		}
		return m, cmd
	}

	newViewer, cmd := m.viewer.Update(msg)
	if v, ok := newViewer.(*viewerModel); ok {
		m.viewer = v
	}
	return m, cmd
}

func (m *appModel) View() tea.View {
	if m.mode == modeList {
		return m.list.View()
	}
	return m.viewer.View()
}

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

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: git seemystared [<user>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Browse starred repos in a boxed TUI.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  (no arg)   → your starred repos (from ~/.gitconfig [seemystared] user)")
		fmt.Fprintln(os.Stderr, "  <user>     → that user's starred repos")
		fmt.Fprintln(os.Stderr, "  @<user>    → force the \"other user\" interpretation")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  git seemystared")
		fmt.Fprintln(os.Stderr, "  git seemystared tsoding")
		fmt.Fprintln(os.Stderr, "  git seemystared @torvalds")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Config (~/.gitconfig):")
		fmt.Fprintln(os.Stderr, "  [seemystared]")
		fmt.Fprintln(os.Stderr, "      user = bakeman-org")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "List TUI keys:")
		fmt.Fprintln(os.Stderr, "  j/k ↑/↓   move         gg/G       top/bottom")
		fmt.Fprintln(os.Stderr, "  ctrl+d/u  half-page    /          search")
		fmt.Fprintln(os.Stderr, "  n/N       next/prev match         f  filter")
		fmt.Fprintln(os.Stderr, "  d         toggle descriptions")
		fmt.Fprintln(os.Stderr, "  esc       clear search (quit if empty)")
		fmt.Fprintln(os.Stderr, "  enter     open README  q  quit")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Viewer TUI keys:")
		fmt.Fprintln(os.Stderr, "  j/k ↑/↓   scroll       ctrl+d/u   half-page  gg/G  top/bottom")
		fmt.Fprintln(os.Stderr, "  /         search       n/N        next/prev match  f  filter")
		fmt.Fprintln(os.Stderr, "  c         copy raw content to clipboard")
		fmt.Fprintln(os.Stderr, "  esc / q   back to list (clears search first if any)")
		fmt.Fprintln(os.Stderr, "  ctrl+c    quit program")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Environment:")
		fmt.Fprintln(os.Stderr, "  GITHUB_TOKEN   GitHub API token (60/hr anonymous → 5000/hr with token)")
	}
	flag.Parse()

	owner := resolveOwner()
	arg := ""
	if flag.NArg() > 0 {
		arg = flag.Arg(0)
	}

	if strings.HasPrefix(arg, "@") {
		owner = strings.TrimPrefix(arg, "@")
		arg = ""
	}

	if arg != "" {
		if strings.Contains(arg, "/") {
			fmt.Fprintf(os.Stderr,
				"Error: %q looks like owner/repo. Direct repo open is gone — open the TUI and use / to search.\n", arg)
			os.Exit(1)
		}
		owner = arg
	}

	repos, err := fetchStarred(owner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching starred repos for %q: %v\n", owner, err)
		os.Exit(1)
	}

	if !term.IsTerminal(int(os.Stdout.Fd())) {
		if len(repos) == 0 {
			fmt.Printf("%s has no starred repos.\n", owner)
			return
		}
		fmt.Printf("Starred repos for %s (%d):\n\n", owner, len(repos))
		for i, r := range repos {
			meta := []string{}
			if r.Stars > 0 {
				meta = append(meta, fmt.Sprintf("★%d", r.Stars))
			}
			if r.Language != "" {
				meta = append(meta, r.Language)
			}
			suffix := ""
			if len(meta) > 0 {
				suffix = "  [" + strings.Join(meta, " · ") + "]"
			}
			fmt.Printf("%3d. %s%s\n", i+1, r.FullName, suffix)
			if r.Description != "" {
				fmt.Printf("     %s\n", r.Description)
			}
		}
		return
	}

	listModel := &starListModel{
		owner:    owner,
		repos:    repos,
		showDesc: true,
	}
	listModel.visible = make([]int, len(repos))
	for i := range repos {
		listModel.visible[i] = i
	}

	app := &appModel{
		owner: owner,
		repos: repos,
		list:  listModel,
	}

	p := tea.NewProgram(app)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
