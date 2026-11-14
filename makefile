# ClaudeBar.app: `make install` builds it, copies it to /Applications and starts it.
# Requires macOS 13+, Go, Xcode Command Line Tools (swiftc).
#
# Contents/MacOS/ClaudeBar      SwiftUI app: menu bar badge + panel (app/ClaudeBar.swift)
# Contents/MacOS/claudebar-cli  Go helper: status line hook, pace math, `json` for the app
# (not "claudebar": APFS is case-insensitive, it would overwrite "ClaudeBar")

# Builds keep the full output, with tool versions, in build/build.log.
SHELL       := /bin/bash
.SHELLFLAGS := -o pipefail -c

LOG      := build/build.log
RUNLOG   := build/run.log
APP      := build/ClaudeBar.app
MACOS    := $(APP)/Contents/MacOS
DEST     := /Applications/ClaudeBar.app
ARCH     := $(shell uname -m)
# Optional: a folder that Claude Code running in a container uses as its ~/.claude.
# `make install CLAUDE_DIR=~/path/to/it` makes the app read it too.
CLAUDE_DIR ?=

.PHONY: help app build-app install install-app logged container run debug stop show test uninstall clean git.push

help:
	@echo "make install    build, copy to /Applications, start (log: $(LOG))"
	@echo "make app        build $(APP) only (log: $(LOG))"
	@echo "make container  inside a Linux container with Claude Code: build the helper, connect the status line"
	@echo "make run        start the installed app"
	@echo "make debug      run the app in the foreground, output to $(RUNLOG) (Ctrl+C to stop)"
	@echo "make stop       stop it"
	@echo "make show       limits as text (no app)"
	@echo "make test       go vet + tests"
	@echo "make uninstall  remove the app and the data file"
	@echo "make clean      remove build/"
	@echo "make git.push   publish this folder as its own GitHub repo (gh), see README"

# Every build writes the full output, with tool versions, to $(LOG).
app:
	@$(MAKE) --no-print-directory logged TARGET=build-app

install:
	@$(MAKE) --no-print-directory logged TARGET=install-app

logged:
	@mkdir -p build
	@{ date; sw_vers 2>&1; uname -m; go version 2>&1; swiftc --version 2>&1; echo; } > $(LOG)
	@$(MAKE) --no-print-directory $(TARGET) 2>&1 | tee -a $(LOG) \
		|| { echo; echo "FAILED. Full log: $(LOG)"; exit 1; }

# Ad-hoc signature, no certificate: macOS runs a build made on the same Mac.
build-app:
	rm -rf $(APP)
	mkdir -p $(MACOS)
	CGO_ENABLED=0 go build -trimpath -o $(MACOS)/claudebar-cli .
	swiftc -parse-as-library -O -target $(ARCH)-apple-macos13.0 -o $(MACOS)/ClaudeBar app/ClaudeBar.swift
	cp Info.plist $(APP)/Contents/Info.plist
	codesign --force --sign - $(MACOS)/claudebar-cli
	codesign --force --sign - $(APP)

install-app: build-app stop
	rm -rf $(DEST)
	cp -R $(APP) $(DEST)
	mkdir -p $(HOME)/.config/claudebar
	if [ -n "$(CLAUDE_DIR)" ]; then echo "$(abspath $(CLAUDE_DIR))" > $(HOME)/.config/claudebar/dirs; fi
	open $(DEST)
	@echo "Done: badge in the top right of the menu bar. Click it, then \"Connect to Claude Code\"."

# Claude Code in a container writes the limits into its own ~/.claude; mount that folder
# from the Mac and pass it to `make install CLAUDE_DIR=...` so the app reads it.
container:
	go build -trimpath -o $(HOME)/.claude/bin/claudebar-cli .
	$(HOME)/.claude/bin/claudebar-cli connect
	@echo "Connected: $(HOME)/.claude/settings.json → $(HOME)/.claude/bin/claudebar-cli statusline"

run:
	open $(DEST)

# Foreground run: crashes and helper errors land in $(RUNLOG).
debug: stop
	@mkdir -p build
	$(DEST)/Contents/MacOS/ClaudeBar 2>&1 | tee $(RUNLOG)

stop:
	@pkill -x ClaudeBar && echo "Stopped ClaudeBar" || true

show:
	$(DEST)/Contents/MacOS/claudebar-cli show

test:
	go vet ./...
	go test ./...

uninstall: stop
	rm -rf $(DEST) $(HOME)/.claude/claudebar.json $(HOME)/.config/claudebar
	@echo "Removed. statusLine in ~/.claude/settings.json: restore settings.json.claudebar-backup or remove it by hand."

clean:
	rm -rf build

# Publishing: this folder as its own repo, without the history of the repo it lives in.
# Only committed files go out (git archive), so build/ and local data never do.
# Files deleted here are dropped there with `git rm` before the new copy is unpacked.
PUBLISH_DIR ?= $(HOME)/Projects/claudebar
GH_REPO     ?= claudebar
GH_DESC     ?= macOS menu bar app: Claude plan usage limits left, pace and per-day budget
MSG         ?= Update
# Author and committer of the published commits; empty: your git config.
NAME        ?=
EMAIL       ?=
TOPLEVEL    := $(shell git rev-parse --show-toplevel)
SUBDIR      := $(patsubst %/,%,$(shell git rev-parse --show-prefix))

git.push: test
	@git diff --quiet HEAD -- . || { echo "Uncommitted changes in this folder: commit them first."; exit 1; }
	@command -v gh >/dev/null || { echo "Install gh: brew install gh, then gh auth login"; exit 1; }
	mkdir -p "$(PUBLISH_DIR)"
	cd "$(PUBLISH_DIR)" && { git rev-parse --git-dir >/dev/null 2>&1 || git init -q -b main; }
	cd "$(PUBLISH_DIR)" && git rm -rq --ignore-unmatch .
	# From the top level: run in a subfolder, git archive keeps only that subfolder's paths.
	git -C "$(TOPLEVEL)" archive HEAD:$(SUBDIR) | tar -x -C "$(PUBLISH_DIR)"
	cd "$(PUBLISH_DIR)" && git add -A && { git diff --cached --quiet || \
		git $(if $(NAME),-c user.name="$(NAME)") $(if $(EMAIL),-c user.email="$(EMAIL)") commit -q -m "$(MSG)"; }
	@cd "$(PUBLISH_DIR)" && echo "Author: $$(git log -1 --format='%an <%ae>')"
	cd "$(PUBLISH_DIR)" && if git remote get-url origin >/dev/null 2>&1; then git push -q origin main; \
		else gh repo create "$(GH_REPO)" --public --source . --push --description "$(GH_DESC)"; fi
	@cd "$(PUBLISH_DIR)" && echo "Published: $$(gh repo view --json url -q .url)"
