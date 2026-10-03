package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

// ---------- 配置与认证 ----------

func newGitHubClient() *github.Client {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return github.NewClient(nil)
	}
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(context.Background(), ts)
	return github.NewClient(tc)
}

// ---------- 数据模型与视图模型 ----------

type Repo struct {
	Name        string
	FullName    string
	Description string
	Stars       int
	URL         string
}

type RepoItem struct {
	repo Repo
}

func (i RepoItem) Title() string { return i.repo.FullName }

func (i RepoItem) Description() string {
	desc := i.repo.Description
	if desc == "" {
		desc = "No description"
	}
	return fmt.Sprintf("⭐ %d  |  %s", i.repo.Stars, desc)
}

func (i RepoItem) FilterValue() string {
	return i.repo.FullName + " " + i.repo.Description
}

// ---------- 消息类型 ----------

type errMsg struct{ err error }
type reposMsg struct{ repos []Repo }

// ---------- 获取数据的命令 ----------

func fetchReposCmd(client *github.Client, username string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		opt := &github.RepositoryListByUserOptions{
			Type:        "public",
			ListOptions: github.ListOptions{PerPage: 100},
		}

		var allRepos []Repo
		for {
			repos, resp, err := client.Repositories.ListByUser(ctx, username, opt)
			if err != nil {
				return errMsg{err}
			}
			for _, r := range repos {
				allRepos = append(allRepos, Repo{
					Name:        r.GetName(),
					FullName:    r.GetFullName(),
					Description: r.GetDescription(),
					Stars:       r.GetStargazersCount(),
					URL:         r.GetHTMLURL(),
				})
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
		return reposMsg{repos: allRepos}
	}
}

// ---------- 主模型 ----------

type model struct {
	list     list.Model
	client   *github.Client
	username string
	err      error
	loading  bool
	quitting bool

	// 新增：悬浮窗相关状态
	width        int
	height       int
	showPopup    bool
	selectedRepo Repo
}

func NewModel(client *github.Client, username string) model {
	l := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	l.Title = fmt.Sprintf("📦 Repos of %s", username)
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.Styles.Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFF")).
		Background(lipgloss.Color("#5A56E0")).
		Padding(0, 1)

	return model{
		list:     l,
		client:   client,
		username: username,
		loading:  true,
	}
}

func (m model) Init() tea.Cmd {
	return fetchReposCmd(m.client, m.username)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// 记录终端宽高，用于居中弹窗
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-2)
		return m, nil

	case reposMsg:
		items := make([]list.Item, len(msg.repos))
		for i, r := range msg.repos {
			items[i] = RepoItem{repo: r}
		}
		m.list.SetItems(items)
		m.loading = false
		return m, nil

	case errMsg:
		m.err = msg.err
		m.loading = false
		return m, nil

	case tea.KeyMsg:
		// ================= 悬浮窗打开时的按键逻辑 =================
		if m.showPopup {
			switch msg.String() {
			case "esc", "q", "enter":
				m.showPopup = false // 关闭悬浮窗，返回列表
			}
			return m, nil // 拦截按键，不传递给底层列表
		}

		// ================= 列表界面的按键逻辑 =================
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if i, ok := m.list.SelectedItem().(RepoItem); ok {
				m.selectedRepo = i.repo
				m.showPopup = true // 打开悬浮窗，而不是打印到终端
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return "Bye!\n"
	}
	if m.err != nil {
		return fmt.Sprintf("❌ Error: %v\n\nPress q to quit.\n", m.err)
	}
	if m.loading {
		return "⏳ Loading repositories...\n"
	}

	// 1. 渲染底层的列表视图
	baseView := "\n" + m.list.View()

	// 2. 如果开启了悬浮窗，则渲染 Neovim 风格的弹窗
	if m.showPopup {
		// 定义弹窗样式：带圆角边框、内边距、特定宽度
		popupStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2).
			Width(60).
			Align(lipgloss.Left)

		// 弹窗内容
		content := fmt.Sprintf(
			"📦 %s\n\n🔗 URL:\n%s\n\n📝 Description:\n%s\n\n⭐ Stars: %d\n\n[Press esc/q/enter to close]",
			m.selectedRepo.FullName,
			m.selectedRepo.URL,
			m.selectedRepo.Description,
			m.selectedRepo.Stars,
		)

		// 渲染弹窗
		popup := popupStyle.Render(content)

		// 使用 lipgloss.Place 将弹窗在整个终端屏幕上居中显示
		// 这会产生类似 Neovim 浮窗覆盖在原界面上的效果
		return lipgloss.Place(
			m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			popup,
		)
	}

	return baseView
}

// ---------- 入口 ----------

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <github-username>\n", os.Args[0])
		os.Exit(1)
	}
	username := strings.TrimSpace(os.Args[1])

	client := newGitHubClient()
	m := NewModel(client, username)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
