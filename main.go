package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
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
// Commit fetching
// ---------------------------------------------------------------------------

type commitItem struct {
	sha     string
	author  string
	date    string
	message string
}

func (c commitItem) Title() string {
	short := c.sha
	if len(short) > 8 {
		short = short[:8]
	}
	return short + "  " + firstLine(c.message)
}

func (c commitItem) Description() string {
	return c.author + " · " + c.date
}

func (c commitItem) FilterValue() string {
	return c.sha + " " + c.message + " " + c.author
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

func fetchCommits(client *scm.Client, owner, repo string, limit int) ([]commitItem, error) {
	ctx := context.Background()

	opts := scm.CommitListOptions{
		Page: 1,
		Size: limit,
	}

	commits, _, err := client.Git.ListCommits(ctx, owner+"/"+repo, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list commits: %w", err)
	}

	items := make([]commitItem, 0, len(commits))
	for _, c := range commits {
		// scm.Signature is a value type (not a pointer), so no nil check is
		// possible. Just read the fields directly.
		author := c.Author.Name
		date := ""
		if !c.Author.Date.IsZero() {
			date = c.Author.Date.Format(time.RFC3339)
		}
		items = append(items, commitItem{
			sha:     c.Sha,
			author:  author,
			date:    date,
			message: c.Message,
		})
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Bubble Tea model
// ---------------------------------------------------------------------------

type model struct {
	list     list.Model
	err      error
	repoName string
}

func newModel(repoName string, items []list.Item) model {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("205")).
		BorderLeftForeground(lipgloss.Color("205"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("205")).
		BorderLeftForeground(lipgloss.Color("205"))

	l := list.New(items, delegate, 0, 0)
	l.Title = fmt.Sprintf("Commits in %s", repoName)
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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: git remote-commit <repository-url>")
		os.Exit(1)
	}

	rawURL := os.Args[1]

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

	// Token is optional. Leave empty for anonymous public API access.
	token := os.Getenv("GIT_REMOTE_COMMIT_TOKEN")

	client, err := newSCMClient(provider, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
		os.Exit(1)
	}

	items, err := fetchCommits(client, owner, repo, 50)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "No commits found (repo may be empty or private).")
		os.Exit(0)
	}

	listItems := make([]list.Item, len(items))
	for i, it := range items {
		listItems[i] = it
	}

	m := newModel(owner+"/"+repo, listItems)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}
