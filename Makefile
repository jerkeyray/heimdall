CYAN    := \033[36m
PURPLE  := \033[35m
BOLD    := \033[1m
GREEN   := \033[32m
RED     := \033[31m
RESET   := \033[0m

.DEFAULT_GOAL := help
.PHONY: help run dev run/openai run/anthropic build test test/v clean

help:
	@printf "$(CYAN) _   _ $(PURPLE)_____ ___ $(CYAN)__  __ ____$(PURPLE)    _    $(CYAN)_     _$(RESET)\n"
	@printf "$(CYAN)| | | |$(PURPLE) ____|_ _|$(CYAN)  \\/  |  _ \\$(PURPLE)  / \\  $(CYAN)| |   | |$(RESET)\n"
	@printf "$(CYAN)| |_| |$(PURPLE)  _|  | |$(CYAN)| |\\/| | | | |$(PURPLE)/ _ \\ $(CYAN)| |   | |$(RESET)\n"
	@printf "$(CYAN)|  _  |$(PURPLE) |___ | |$(CYAN)| |  | | |_| $(PURPLE)/ ___ \\$(CYAN)| |___| |___$(RESET)\n"
	@printf "$(CYAN)|_| |_|$(PURPLE)_____|___|$(CYAN)_|  |_|____/$(PURPLE)_/   \\_\\$(CYAN)_____|_____|$(RESET)\n"
	@printf "\n"
	@printf "$(BOLD)Usage:$(RESET)\n"
	@printf "  make $(GREEN)<target>$(RESET)\n"
	@printf "\n"
	@printf "$(BOLD)Targets:$(RESET)\n"
	@printf "  $(GREEN)help$(RESET)           Print this banner and target list\n"
	@printf "  $(GREEN)run$(RESET)            Run the server (env vars must be set)\n"
	@printf "  $(GREEN)dev$(RESET)            Run with hot reload via air\n"
	@printf "  $(GREEN)run/openai$(RESET)     Run with OPENAI_API_KEY guard\n"
	@printf "  $(GREEN)run/anthropic$(RESET)  Run with ANTHROPIC_API_KEY guard\n"
	@printf "  $(GREEN)build$(RESET)          Build binary to bin/heimdall\n"
	@printf "  $(GREEN)test$(RESET)           Run tests with pass/fail summary\n"
	@printf "  $(GREEN)test/v$(RESET)         Run tests verbose\n"
	@printf "  $(GREEN)clean$(RESET)          Remove bin/ directory\n"

run:
	@go run ./cmd/heimdall/

dev:
	@which air > /dev/null 2>&1 || (printf "$(RED)✗ air not found — run: go install github.com/air-verse/air@latest$(RESET)\n" && exit 1)
	@air

ifndef OPENAI_API_KEY
run/openai:
	@printf "$(RED)✗ OPENAI_API_KEY is not set$(RESET)\n"
	@exit 1
else
run/openai:
	@go run ./cmd/heimdall/
endif

ifndef ANTHROPIC_API_KEY
run/anthropic:
	@printf "$(RED)✗ ANTHROPIC_API_KEY is not set$(RESET)\n"
	@exit 1
else
run/anthropic:
	@go run ./cmd/heimdall/
endif

build:
	@mkdir -p bin
	@go build -o bin/heimdall ./cmd/heimdall/
	@printf "$(GREEN)✓ built bin/heimdall$(RESET)\n"

test:
	@go test ./... && printf "$(GREEN)✓ all tests passed$(RESET)\n" || (printf "$(RED)✗ tests failed$(RESET)\n"; exit 1)

test/v:
	@go test ./... -v

clean:
	@rm -rf bin/
	@printf "$(GREEN)✓ removed bin/$(RESET)\n"
