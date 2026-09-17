package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const proxyPrefix = "https://gh-proxy.org/"

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: git proxy-clone [git clone options] <repository>")
		os.Exit(1)
	}

	// 检查是否包含 help
	for _, a := range args {
		if a == "-h" || a == "--help" {
			printHelp()
			return
		}
	}

	// 找到第一个看起来像 URL 的参数（不以 - 开头，且包含 github.com 或 http）
	urlIndex := -1
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.Contains(a, "github.com") || strings.HasPrefix(a, "http") {
			urlIndex = i
			break
		}
	}

	if urlIndex == -1 {
		fmt.Fprintln(os.Stderr, "error: no repository URL found in arguments")
		os.Exit(1)
	}

	originalURL := args[urlIndex]

	// 如果已经带了代理前缀，不再重复添加
	if strings.HasPrefix(originalURL, proxyPrefix) {
		// 直接透传
	} else {
		args[urlIndex] = proxyPrefix + originalURL
	}

	// 构建 git clone 命令
	gitArgs := append([]string{"clone"}, args...)

	cmd := exec.Command("git", gitArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "git clone failed: %v\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`git proxy-clone - Clone GitHub repositories through gh-proxy.org

Usage:
    git proxy-clone [git clone options] <repository>

Examples:
    git proxy-clone https://github.com/tsoding/nob.h
    git proxy-clone --depth=1 https://github.com/tsoding/nob.h
    git proxy-clone --depth=1 --branch=main https://github.com/tsoding/nob.h

All arguments after "proxy-clone" are forwarded to "git clone".
The repository URL is automatically prefixed with https://gh-proxy.org/`)
}
