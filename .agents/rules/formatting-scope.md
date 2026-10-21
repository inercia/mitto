# Formatting scope

- `make fmt` runs `go fmt ./...` across the repository. Before running it in a working tree with unrelated edits, check the formatter's scope; use a clean snapshot or format only the in-scope files so unrelated work is not rewritten.
