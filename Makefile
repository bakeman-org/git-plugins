BINARY := git-readme
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GCFLAGS := -trimpath

.PHONY: build build-release install test clean

# 开发构建：保留调试信息，快速迭代
build:
	go build -trimpath -o $(BINARY) ./cmd/v1

# 发布构建：去符号表、去 DWARF、去路径
build-release:
	CGO_ENABLED=0 go build \
		-trimpath \
		-ldflags="$(LDFLAGS)" \
		-gcflags="all=-l=4" \
		-o $(BINARY) \
		./cmd/v1
	@echo "size: $$(du -h $(BINARY) | cut -f1)"

clean:
	rm -f $(BINARY)

upx:
	upx --best --lzma -o git-readme.upx git-readme