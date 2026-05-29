## Summary

Fixes a checkpoint race where the timer goroutine could mutate pending SDK ops while the handler appended or returned them.

## Details

- Locks pending op writes/removals and returns a cloned op slice.
- Starts only one checkpoint interval timer per batch.

## Testing

- `go test -race ./internal/checkpoint ./internal/sdkrequest`
- `go test -race ./tests/golang -run TestFnCheckpoint -count=1 -v` in `inngest`
