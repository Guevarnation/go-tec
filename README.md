# Log Aggregator

A concurrent log file merger that reads multiple log sources in parallel, merges entries chronologically, and computes statistics. Standard library only.

## Usage

```bash
go run main.go auth.log api.log db.log
```

## Log Format

```
2024-01-15T10:04:01Z ERROR service=auth message="invalid token"
```

Each line has: `<RFC3339 timestamp> <LEVEL> service=<name> message="<text>"`

## How It Works

The program flows through 4 stages: **Parse → Collect → Sort → Stats**.

### Functions

#### `parseLine(line string) (LogEntry, error)`
Parses a single log line. Splits by spaces to extract the timestamp and level, then iterates over `key=value` pairs to extract `service` and `message`. Handles quoted values (with spaces inside). Returns an error for any malformed line.

#### `parseReader(r io.Reader) ([]LogEntry, error)`
Wraps a `bufio.Scanner` around a reader and calls `parseLine` for each line. Malformed lines are skipped with a warning to stderr. If the underlying reader fails (e.g., disk error), the error is returned alongside any entries that were successfully parsed before the failure.

#### `Merge(sources []io.Reader) ([]LogEntry, Stats, error)`
Entry point. Delegates to `MergeWithFilter` with a nil filter.

#### `MergeWithFilter(sources []io.Reader, keep func(LogEntry) bool) ([]LogEntry, Stats, error)`
Core function that orchestrates everything:

1. **Concurrent parsing** — Spawns one goroutine per `io.Reader`. Each goroutine calls `parseReader` and writes its result to a pre-allocated slot in a `[]result` slice (indexed by goroutine number).
2. **Synchronization** — A `sync.WaitGroup` blocks until all goroutines finish. No goroutine outlives the function call.
3. **Collection** — Iterates over the results slice, appending all entries into one flat slice and collecting any reader errors.
4. **Filtering** — If a `keep` function is provided, entries that don't match are discarded.
5. **Sorting** — `sort.Slice` orders entries chronologically. Entries with the same timestamp are sorted alphabetically by service name.
6. **Stats** — `computeStats` does a single pass over the sorted slice to populate all `Stats` fields.

#### `computeStats(entries []LogEntry) Stats`
Single loop over the sorted entries. Sets `FirstEntry` and `LastEntry` from the first/last elements. Counts entries by level and service using maps.

#### `main()`
Opens files from `os.Args[1:]`, passes them as `[]io.Reader` to `Merge`, and prints the formatted output.

## Design Decisions

### Pre-allocated slice instead of channels
Each goroutine writes to `results[idx]` — its own dedicated slot. This eliminates the need for a mutex during concurrent parsing and avoids channel complexity. After `wg.Wait()`, the main goroutine reads all slots sequentially. This is race-free by construction since no two goroutines share an index.

### Collect-then-sort instead of k-way merge
A k-way merge (using a heap) would be more efficient for very large inputs since individual files are often pre-sorted. However, `sort.Slice` on the combined slice is simpler, correct regardless of input order, and sufficient for the expected scale. The O(n log n) sort is the simplest approach that meets all requirements.

### Errors are collected, not fatal
Reader errors are accumulated and returned alongside whatever entries were successfully parsed. This means a single failing source doesn't prevent results from other sources. The caller decides how to handle the error.

### Filter applied before sorting
Filtering before sort reduces the number of elements to sort, which is a minor optimization. Stats are computed after filtering, so they reflect only the entries that passed the filter.

## Running Tests

```bash
go test -race -v ./...
```

### Test Coverage

| Test | Edge Case |
|---|---|
| `ParseLine_Valid` | Standard line parsing |
| `ParseLine_EmptyMessage` | `message=""` |
| `ParseLine_Malformed` | Empty, bad timestamp, missing fields |
| `Merge_BasicThreeSources` | Multi-source merge, full stats, ordering |
| `Merge_EmptyReader` | Reader with no content |
| `Merge_NoSources` | Zero readers |
| `Merge_SingleReader` | Out-of-order lines in one file |
| `Merge_MalformedLinesSkipped` | Garbage lines mixed with valid |
| `Merge_DuplicateEntries` | Identical entries from different sources |
| `Merge_ManySources` | 10 concurrent readers |
| `Merge_SameTimestampTieBreaking` | Alphabetical service sort on ties |
| `Merge_MessageWithSpaces` | Multi-word quoted message |
| `Merge_ReaderError` | Reader fails mid-read, error propagated |
| `Merge_AllReadersError` | All readers fail |
| `MergeWithFilter_ErrorsOnly` | Filter by level |
| `MergeWithFilter_NilFilter` | Nil filter = no filtering |
| `MergeWithFilter_ServiceFilter` | Filter by service |
# go-tec
