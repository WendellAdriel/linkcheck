# Linkcheck

A Go CLI for checking a reviewed list of URLs. Uses only the standard library, with bounded concurrency, paced dispatch, timeouts, and cancellation.

Companion to [Building a Link Checker in Go Without Overwhelming the Network](https://wendelladriel.com/blog/building-a-link-checker-in-go-without-overwhelming-the-network).

## Run

Requires Go 1.27.3 or newer. Put absolute HTTP or HTTPS URLs in `urls.txt`, one per line. See `urls.example.txt` for the format.

```bash
go build -o linkcheck .
./linkcheck -workers=4 -interval=500ms -timeout=8s -budget=3m urls.txt > report.jsonl
```

Flags must appear before the filename. Results go to stdout as JSON Lines. The summary goes to stderr. Ctrl+C or `SIGTERM` cancels the scan.

| Exit code | Meaning |
| --- | --- |
| `0` | All checks returned successful HTTP statuses. |
| `1` | At least one link is broken or needs review. |
| `2` | Invalid input, an operational error, or an incomplete scan. |

Blank lines are ignored. URL fragments are removed before deduplication. `404` and `410` are marked `broken`. Other unsuccessful checks are marked `review`.

## Test

```bash
go test -race -count=10 ./...
go vet ./...
```

## Limits

Use only trusted URL lists. This is not a public URL checking service and does not block private network destinations. It checks HTTP reachability, not page content or fragment targets. Input stays in memory, and dispatch pacing is not a strict per-host request rate limit because redirects can send additional requests.
