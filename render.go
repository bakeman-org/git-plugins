package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	descStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Italic(true)
)

// RenderMarkdown 使用 glamour 渲染 Markdown，自动适配终端明暗风格。
func RenderMarkdown(md string) (string, error) {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		// 非 TTY 或检测失败时回退到 notty 风格
		r, err = glamour.NewTermRenderer(
			glamour.WithStandardStyle("notty"),
			glamour.WithWordWrap(80),
		)
		if err != nil {
			return "", err
		}
	}
	return r.Render(md)
}

// RenderMeta 渲染仓库元信息（README 缺失时的降级输出）。
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
