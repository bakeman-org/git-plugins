package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/drone/go-scm/scm"
	"github.com/drone/go-scm/scm/driver/gitea"
	"github.com/drone/go-scm/scm/driver/gitee"
	"github.com/drone/go-scm/scm/driver/github"
)

// ---------------------------------------------------------------------------
// Provider detection and client creation
// ---------------------------------------------------------------------------

type Provider string

const (
	GitHub  Provider = "github"
	Gitea   Provider = "gitea"
	Gitee   Provider = "gitee"
	Unknown Provider = "unknown"
)

func detectProvider(rawURL string) Provider {
	switch {
	case strings.Contains(rawURL, "github.com"):
		return GitHub
	case strings.Contains(rawURL, "codeberg.org"):
		return Gitea
	case strings.Contains(rawURL, "gitee.com"):
		return Gitee
	default:
		return Unknown
	}
}

func parseOwnerRepo(rawURL string) (owner, repo string, err error) {
	u := strings.TrimPrefix(rawURL, "https://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimSuffix(u, ".git")
	parts := strings.Split(u, "/")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("invalid repository URL: %s", rawURL)
	}
	return parts[1], parts[2], nil
}

func newSCMClient(p Provider, token string) (*scm.Client, error) {
	var client *scm.Client
	var err error

	switch p {
	case GitHub:
		client = github.NewDefault()
	case Gitea:
		client, err = gitea.New("https://codeberg.org")
	case Gitee:
		client, err = gitee.New("https://gitee.com/api/v5")
	default:
		return nil, fmt.Errorf("unsupported provider")
	}
	if err != nil {
		return nil, err
	}

	// Token is optional. Only attach auth transport if a token is set.
	if token != "" {
		base := client.Client
		client.Client = &http.Client{
			Transport: &authTransport{base: base.Transport, token: token},
		}
	}
	return client, nil
}

type authTransport struct {
	base  http.RoundTripper
	token string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "token "+t.token)
	if t.base != nil {
		return t.base.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// ---------------------------------------------------------------------------
// Commit model
// ---------------------------------------------------------------------------

const (
	dateTimeLayout = "2006-01-02 15:04"
	dayLayout      = "2006-01-02"
)

type commitItem struct {
	sha     string
	author  string
	date    time.Time
	message string
}

func (c commitItem) Title() string {
	short := c.sha
	if len(short) > 8 {
		short = short[:8]
	}
	return short + "  " + firstLine(c.message)
}

// Description is shown under each item. Date first so that commits on the
// same day visually align into a group when the list is sorted by date.
func (c commitItem) Description() string {
	parts := make([]string, 0, 2)
	if !c.date.IsZero() {
		parts = append(parts, c.date.Format(dateTimeLayout))
	}
	if c.author != "" {
		parts = append(parts, c.author)
	}
	return strings.Join(parts, "  ·  ")
}

// FilterValue powers the fuzzy filter (default key: /). It includes SHA,
// author, date (both full timestamp and day-only), and the commit message
// so that grep matches any of them.
func (c commitItem) FilterValue() string {
	var b strings.Builder
	b.WriteString(c.sha)
	b.WriteByte(' ')
	if c.author != "" {
		b.WriteString(c.author)
		b.WriteByte(' ')
	}
	if !c.date.IsZero() {
		b.WriteString(c.date.Format(dateTimeLayout))
		b.WriteByte(' ')
		b.WriteString(c.date.Format(dayLayout))
		b.WriteByte(' ')
	}
	b.WriteString(c.message)
	return b.String()
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// ---------------------------------------------------------------------------
// Commit fetching (paginated, branch-aware, sorted)
// ---------------------------------------------------------------------------

type sortMode string

const (
	sortDateDesc sortMode = "date-desc"
	sortDateAsc  sortMode = "date-asc"
	sortAuthor   sortMode = "author"
)

func parseSortMode(s string) (sortMode, error) {
	switch strings.ToLower(s) {
	case "", "date-desc", "desc", "newest":
		return sortDateDesc, nil
	case "date-asc", "asc", "oldest":
		return sortDateAsc, nil
	case "author":
		return sortAuthor, nil
	default:
		return "", fmt.Errorf("unknown sort mode %q", s)
	}
}

func applySort(items []commitItem, mode sortMode) {
	switch mode {
	case sortDateAsc:
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].date.Before(items[j].date)
		})
	case sortAuthor:
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].author != items[j].author {
				return items[i].author < items[j].author
			}
			return items[i].date.After(items[j].date)
		})
	default: // sortDateDesc
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].date.After(items[j].date)
		})
	}
}

// fetchCommitPage fetches a single page of commits.
func fetchCommitPage(client *scm.Client, owner, repo string, page, size int, branch string) ([]commitItem, bool, error) {
	ctx := context.Background()
	opts := scm.CommitListOptions{
		Ref:  branch,
		Page: page,
		Size: size,
	}

	commits, resp, err := client.Git.ListCommits(ctx, owner+"/"+repo, opts)
	if err != nil {
		return nil, false, fmt.Errorf("failed to list commits (page %d): %w", page, err)
	}

	items := make([]commitItem, 0, len(commits))
	for _, c := range commits {
		// Author/Committer fallback: Codeberg (Gitea) often leaves
		// Author.Date zero while Committer.Date is populated.
		author := c.Author.Name
		if author == "" {
			author = c.Committer.Name
		}
		t := c.Author.Date
		if t.IsZero() {
			t = c.Committer.Date
		}
		items = append(items, commitItem{
			sha:     c.Sha,
			author:  author,
			date:    t,
			message: c.Message,
		})
	}

	hasMore := len(commits) == size
	if resp != nil && resp.Page.Next > 0 {
		hasMore = true
	}
	return items, hasMore, nil
}

// fetchAllCommits pages through the API until limit is reached or exhausted,
// then sorts the accumulated list.
func fetchAllCommits(client *scm.Client, owner, repo string, limit int, branch string, mode sortMode) ([]commitItem, error) {
	const pageSize = 50 // safe for all three providers
	var all []commitItem
	var lastErr error
	page := 1

	for len(all) < limit {
		items, hasMore, err := fetchCommitPage(client, owner, repo, page, pageSize, branch)
		if err != nil {
			lastErr = err
			break
		}
		if len(items) == 0 {
			break
		}
		all = append(all, items...)
		if !hasMore {
			break
		}
		page++
	}

	if len(all) > limit {
		all = all[:limit]
	}

	applySort(all, mode)
	return all, lastErr
}

// ---------------------------------------------------------------------------
// Bubble Tea model
// ---------------------------------------------------------------------------

type model struct {
	list     list.Model
	err      error
	repoName string
	refName  string
}

func newModel(repoName, refName string, items []list.Item) model {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("205")).
		BorderLeftForeground(lipgloss.Color("205"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("205")).
		BorderLeftForeground(lipgloss.Color("205"))

	l := list.New(items, delegate, 0, 0)
	l.Title = fmt.Sprintf("Commits in %s (%s) — %d", repoName, refName, len(items))
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(true)
	l.Styles.Title = lipgloss.NewStyle().
		MarginLeft(2).
		Foreground(lipgloss.Color("205")).
		Bold(true)

	return model{
		list:     l,
		repoName: repoName,
		refName:  refName,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := docStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)

	case tea.KeyMsg:
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.err != nil {
		return docStyle.Render("Error: " + m.err.Error())
	}
	return docStyle.Render(m.list.View())
}

var docStyle = lipgloss.NewStyle().Margin(1, 2)

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: git remote-commit [options] <repository-url>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -n, --limit N     maximum number of commits to fetch (default 200)")
	fmt.Fprintln(os.Stderr, "  -b, --branch B    branch or ref to fetch commits from (default: default branch)")
	fmt.Fprintln(os.Stderr, "  -s, --sort MODE   sort order: date-desc (default), date-asc, author")
	fmt.Fprintln(os.Stderr, "  -h, --help        show this help")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Environment:")
	fmt.Fprintln(os.Stderr, "  GIT_REMOTE_COMMIT_TOKEN   optional personal access token for higher rate limits")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Filtering (inside TUI):")
	fmt.Fprintln(os.Stderr, "  Press / then type to filter by SHA, author, date, or message.")
	fmt.Fprintln(os.Stderr, "  Try: 'alice', '2026-09-17', 'fix', or a combination.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintln(os.Stderr, "  git remote-commit https://github.com/octocat/Hello-World")
	fmt.Fprintln(os.Stderr, "  git remote-commit -b develop -n 500 https://github.com/user/repo")
	fmt.Fprintln(os.Stderr, "  git remote-commit -s author https://codeberg.org/forgejo/forgejo")
}

func main() {
	var rawURL string
	var branch string
	limit := 200
	mode := sortDateDesc

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			usage()
			os.Exit(0)
		case "-n", "--limit":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: -n/--limit requires a value")
				os.Exit(1)
			}
			if _, err := fmt.Sscanf(args[i+1], "%d", &limit); err != nil || limit <= 0 {
				fmt.Fprintf(os.Stderr, "error: invalid limit %q\n", args[i+1])
				os.Exit(1)
			}
			i++
		case "-b", "--branch":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: -b/--branch requires a value")
				os.Exit(1)
			}
			branch = args[i+1]
			i++
		case "-s", "--sort":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: -s/--sort requires a value")
				os.Exit(1)
			}
			m, err := parseSortMode(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			mode = m
			i++
		default:
			if rawURL == "" {
				rawURL = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n", args[i])
				usage()
				os.Exit(1)
			}
		}
	}

	if rawURL == "" {
		usage()
		os.Exit(1)
	}

	provider := detectProvider(rawURL)
	if provider == Unknown {
		fmt.Fprintf(os.Stderr, "Unsupported provider: %s\n", rawURL)
		os.Exit(1)
	}

	owner, repo, err := parseOwnerRepo(rawURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing URL: %v\n", err)
		os.Exit(1)
	}

	token := os.Getenv("GIT_REMOTE_COMMIT_TOKEN")
	client, err := newSCMClient(provider, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
		os.Exit(1)
	}

	displayRef := branch
	if displayRef == "" {
		displayRef = "default branch"
	}
	fmt.Fprintf(os.Stderr, "Fetching up to %d commits from %s/%s (%s), sort=%s ...\n",
		limit, owner, repo, displayRef, mode)

	items, err := fetchAllCommits(client, owner, repo, limit, branch, mode)
	if err != nil {
		if len(items) == 0 {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Warning: %v (showing %d commits)\n", err, len(items))
	}

	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "No commits found (repo may be empty, private, or branch does not exist).")
		os.Exit(0)
	}

	listItems := make([]list.Item, len(items))
	for i, it := range items {
		listItems[i] = it
	}

	m := newModel(owner+"/"+repo, displayRef, listItems)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}
