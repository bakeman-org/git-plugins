package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	defaultProxy = "https://gh-proxy.org/"
	logPrefix    = "[proxy-clone] "
)

type Config struct {
	Proxy   string
	Verbose bool
}

func main() {
	args := os.Args[1:]

	// 提前处理特殊命令
	for _, a := range args {
		switch a {
		case "-h", "--help":
			printHelp()
			return
		case "--init":
			runInit()
			return
		}
	}

	cfg := loadConfig()

	// 解析自定义 flag，其余透传给 git clone
	var (
		passThrough []string
		printOnly   bool
	)

	for _, a := range args {
		switch {
		case a == "-v" || a == "--verbose":
			cfg.Verbose = true
		case a == "--print-url":
			printOnly = true
		case strings.HasPrefix(a, "--proxy="):
			cfg.Proxy = strings.TrimPrefix(a, "--proxy=")
		case a == "--no-proxy":
			cfg.Proxy = ""
		default:
			passThrough = append(passThrough, a)
		}
	}

	// 找到仓库 URL
	urlIndex := -1
	for i, a := range passThrough {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if looksLikeURL(a) {
			urlIndex = i
			break
		}
	}

	if urlIndex == -1 {
		fatal("no repository URL found in arguments (see --help)")
	}

	originalURL := passThrough[urlIndex]
	finalURL := originalURL

	if cfg.Proxy != "" && !strings.HasPrefix(originalURL, cfg.Proxy) {
		finalURL = cfg.Proxy + originalURL
		passThrough[urlIndex] = finalURL
	}

	// 打印日志
	if cfg.Verbose || printOnly {
		logLine("original=%s", originalURL)
		logLine("proxy=%s", cfg.Proxy)
		logLine("resolved=%s", finalURL)
	}

	if printOnly {
		fmt.Println(finalURL)
		return
	}

	if cfg.Verbose {
		logLine("exec=git clone %s", strings.Join(passThrough, " "))
	}

	gitArgs := append([]string{"clone"}, passThrough...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fatal(err.Error())
	}
}

// ---------- 配置 ----------

func loadConfig() Config {
	cfg := Config{Proxy: defaultProxy}

	if v := gitConfigGet("proxyclone.proxy"); v != "" {
		cfg.Proxy = v
	}
	if v := gitConfigGet("proxyclone.verbose"); isTruthy(v) {
		cfg.Verbose = true
	}

	// 环境变量覆盖
	if v := os.Getenv("GIT_PROXY_CLONE_PROXY"); v != "" {
		cfg.Proxy = v
	}
	if isTruthy(os.Getenv("GIT_PROXY_CLONE_VERBOSE")) {
		cfg.Verbose = true
	}

	return cfg
}

func gitConfigGet(key string) string {
	out, err := exec.Command("git", "config", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func runInit() {
	type kv struct{ k, v string }
	items := []kv{
		{"proxyclone.proxy", defaultProxy},
		{"proxyclone.verbose", "true"},
	}
	for _, it := range items {
		cmd := exec.Command("git", "config", "--global", it.k, it.v)
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to set %s: %v\n", it.k, err)
			os.Exit(1)
		}
		fmt.Printf("set %s = %s\n", it.k, it.v)
	}
	fmt.Println("done. check with: git config --global --get-regexp '^proxyclone\\.'")
}

// ---------- 工具 ----------

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasPrefix(s, "ssh://") ||
		strings.HasSuffix(s, ".git")
}

func logLine(format string, a ...any) {
	fmt.Fprintf(os.Stderr, logPrefix+format+"\n", a...)
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}

func printHelp() {
	fmt.Print(`git proxy-clone - Clone repositories through a git proxy

Usage:
    git proxy-clone [options] [git clone options] <repository>

Options:
    -v, --verbose        Print verbose logs to stderr
        --print-url      Print the resolved URL and exit (no clone)
        --proxy=URL      Override proxy prefix (default: https://gh-proxy.org/)
        --no-proxy       Disable proxy (pass through to git clone)
        --init           Write default proxyclone.* settings to global gitconfig
    -h, --help           Show this help

Configuration (git config):
    proxyclone.proxy     Proxy prefix, e.g. https://gh-proxy.org/
    proxyclone.verbose   true/false — always print verbose logs

Environment (higher priority than git config):
    GIT_PROXY_CLONE_PROXY
    GIT_PROXY_CLONE_VERBOSE

Examples:
    git proxy-clone --depth=1 https://github.com/tsoding/nob.h
    git proxy-clone -v --depth=1 https://github.com/vricosti/ruzu-emu
    git proxy-clone --proxy=https://mirror.ghproxy.com/ https://github.com/x/y
    git proxy-clone --print-url https://github.com/x/y
    git proxy-clone --init
`)
}
