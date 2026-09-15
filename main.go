package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func main() {
	descOnly := flag.Bool("desc", false, "只显示仓库描述，不拉取 README")
	rawOnly := flag.Bool("raw", false, "输出原始 Markdown，不做终端渲染")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: git readme [--desc] [--raw] <repository-url>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  git readme https://github.com/aidevjoe/Flip-Clock")
		fmt.Fprintln(os.Stderr, "  git readme --desc https://codeberg.org/forgejo/forgejo")
		fmt.Fprintln(os.Stderr, "  git readme --raw https://gitee.com/mirrors/redis")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Environment:")
		fmt.Fprintln(os.Stderr, "  GITHUB_TOKEN  GitHub API token（提高速率限制/访问私有仓库）")
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

	// 明确只看描述：直接拉 meta，不做 README 回退
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
		// 有 README：渲染或原样输出
		if *rawOnly {
			fmt.Print(readme)
			return
		}
		out, rErr := RenderMarkdown(readme)
		if rErr != nil {
			fmt.Fprintf(os.Stderr, "Error rendering README: %v\n", rErr)
			os.Exit(1)
		}
		fmt.Print(out)

	case errors.Is(err, ErrReadmeNotFound):
		// 没 README：降级到仓库描述
		meta, mErr := FetchMeta(info)
		if mErr != nil {
			fmt.Fprintf(os.Stderr,
				"No README found, and failed to fetch repo info: %v\n", mErr)
			os.Exit(1)
		}
		if meta.Description == "" {
			fmt.Fprintf(os.Stderr,
				"%s has no README and no description.\n", meta.FullName)
			os.Exit(2)
		}
		fmt.Print(RenderMeta(meta))

	default:
		// 其他错误（网络/认证/限流）直接报错，不回退
		fmt.Fprintf(os.Stderr, "Error fetching README: %v\n", err)
		os.Exit(1)
	}
}
