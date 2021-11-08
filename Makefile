.PHONY: build
build:
	@go build -o bin/ github.com/hulucc/pkg-fs/...
	
.PHONY: run
run: build
	@bin/pkg-fs
