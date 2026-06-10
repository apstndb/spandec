# Thin compatibility wrapper; tasks and tool versions live in mise.toml.
.PHONY: build check fmt fmt-check lint test test-race vet

build check fmt fmt-check lint test test-race vet:
	mise run $@
