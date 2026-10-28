.PHONY: install dev clean

BINDIR ?= $(HOME)/.local/bin
DEV_HOME ?= $(HOME)/.recurdev

# Build recur into BINDIR (default ~/.local/bin) so it can be run from anywhere.
install:
	mkdir -p $(BINDIR)
	go build -o $(BINDIR)/recur .
	@echo "installed $(BINDIR)/recur"

# Run from source with an isolated dev home (settings, prompts, sessions).
dev:
	RECUR_HOME=$(DEV_HOME) go run . $(ARGS)

# Remove the dev home.
clean:
	rm -rf $(DEV_HOME)
