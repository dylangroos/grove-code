# grove

TUI agent / worktree / diff manager.

## Install

Requires Go 1.25+.

```sh
go install github.com/dylangroos/grove-code/cmd/grove@latest
export PATH=$(go env GOPATH)/bin:$PATH   # one-time; add to ~/.bashrc (or ~/.zshrc) to persist
```

## Run

From inside any git checkout:

```sh
grove
```

## Build from source

```sh
git clone https://github.com/dylangroos/grove-code
cd grove-code
make install
```
