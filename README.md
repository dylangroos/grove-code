# grove

TUI agent / worktree / diff manager.

## Install

```sh
go install github.com/dylangroos/grove-code/cmd/grove@latest
```

This drops a `grove` binary in `$(go env GOPATH)/bin`. Make sure that's on your `PATH`.

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
