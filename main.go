package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Timestamp time.Time
	Level     string // INFO, WARN, ERROR
	Service   string
	Message   string
}

type Stats struct {
	TotalEntries   int
	CountByLevel   map[string]int
	CountByService map[string]int
	FirstEntry     time.Time
	LastEntry      time.Time
}

// parseLine parses a single log line into a LogEntry.
// Format: 2024-01-15T10:04:01Z ERROR service=auth message="invalid token"
func parseLine(line string) (LogEntry, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return LogEntry{}, fmt.Errorf("empty line")
	}

	// Find timestamp (first field)
	spaceIdx := strings.IndexByte(line, ' ')
	if spaceIdx == -1 {
		return LogEntry{}, fmt.Errorf("malformed line: no space found")
	}
	ts, err := time.Parse(time.RFC3339, line[:spaceIdx])
	if err != nil {
		return LogEntry{}, fmt.Errorf("bad timestamp: %w", err)
	}
	rest := line[spaceIdx+1:]

	// Find level (second field)
	spaceIdx = strings.IndexByte(rest, ' ')
	if spaceIdx == -1 {
		return LogEntry{}, fmt.Errorf("malformed line: no level")
	}
	level := rest[:spaceIdx]
	rest = rest[spaceIdx+1:]

	// Parse key=value pairs for service and message
	var service, message string
	for rest != "" {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			break
		}

		eqIdx := strings.IndexByte(rest, '=')
		if eqIdx == -1 {
			break
		}
		key := rest[:eqIdx]
		rest = rest[eqIdx+1:]

		var value string
		if len(rest) > 0 && rest[0] == '"' {
			// Quoted value — find closing quote
			closeIdx := strings.IndexByte(rest[1:], '"')
			if closeIdx == -1 {
				value = rest[1:]
				rest = ""
			} else {
				value = rest[1 : closeIdx+1]
				rest = rest[closeIdx+2:]
			}
		} else {
			// Unquoted value — until next space
			spaceIdx = strings.IndexByte(rest, ' ')
			if spaceIdx == -1 {
				value = rest
				rest = ""
			} else {
				value = rest[:spaceIdx]
				rest = rest[spaceIdx+1:]
			}
		}

		switch key {
		case "service":
			service = value
		case "message":
			message = value
		}
	}

	if service == "" {
		return LogEntry{}, fmt.Errorf("malformed line: missing service")
	}

	return LogEntry{
		Timestamp: ts,
		Level:     level,
		Service:   service,
		Message:   message,
	}, nil
}

// parseReader reads all lines from r and returns parsed entries.
// Malformed lines are skipped with a warning to stderr.
func parseReader(r io.Reader) ([]LogEntry, error) {
	var entries []LogEntry
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		entry, err := parseLine(scanner.Text())
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping malformed line: %v\n", err)
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return entries, fmt.Errorf("reader error: %w", err)
	}
	return entries, nil
}

// computeStats computes statistics from a sorted slice of entries.
func computeStats(entries []LogEntry) Stats {
	stats := Stats{
		TotalEntries:   len(entries),
		CountByLevel:   make(map[string]int),
		CountByService: make(map[string]int),
	}
	if len(entries) > 0 {
		stats.FirstEntry = entries[0].Timestamp
		stats.LastEntry = entries[len(entries)-1].Timestamp
	}
	for _, e := range entries {
		stats.CountByLevel[e.Level]++
		stats.CountByService[e.Service]++
	}
	return stats
}

// Merge reads from multiple io.Readers concurrently, merges all log
// entries in chronological order, and returns them along with stats.
func Merge(sources []io.Reader) ([]LogEntry, Stats, error) {
	return MergeWithFilter(sources, nil)
}

// MergeWithFilter reads from multiple io.Readers concurrently, merges
// entries that pass the keep filter, and returns them with stats.
func MergeWithFilter(sources []io.Reader, keep func(LogEntry) bool) ([]LogEntry, Stats, error) {
	type result struct {
		entries []LogEntry
		err     error
	}

	results := make([]result, len(sources))
	var wg sync.WaitGroup

	for i, r := range sources {
		wg.Add(1)
		go func(idx int, reader io.Reader) {
			defer wg.Done()
			entries, err := parseReader(reader)
			results[idx] = result{entries: entries, err: err}
		}(i, r)
	}

	wg.Wait()

	// Collect all entries and check for errors
	var allEntries []LogEntry
	var errs []string
	for _, res := range results {
		if res.err != nil {
			errs = append(errs, res.err.Error())
		}
		allEntries = append(allEntries, res.entries...)
	}

	// Apply filter if provided
	if keep != nil {
		filtered := make([]LogEntry, 0, len(allEntries))
		for _, e := range allEntries {
			if keep(e) {
				filtered = append(filtered, e)
			}
		}
		allEntries = filtered
	}

	// Sort: chronological, then alphabetical by service for ties
	sort.Slice(allEntries, func(i, j int) bool {
		if allEntries[i].Timestamp.Equal(allEntries[j].Timestamp) {
			return allEntries[i].Service < allEntries[j].Service
		}
		return allEntries[i].Timestamp.Before(allEntries[j].Timestamp)
	})

	stats := computeStats(allEntries)

	var err error
	if len(errs) > 0 {
		err = fmt.Errorf("reader errors: %s", strings.Join(errs, "; "))
	}

	return allEntries, stats, err
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go-tec <logfile1> [logfile2] ...")
		os.Exit(1)
	}

	var readers []io.Reader
	var files []*os.File
	for _, path := range os.Args[1:] {
		f, err := os.Open(path)
		if err != nil {
			log.Fatalf("failed to open %s: %v", path, err)
		}
		files = append(files, f)
		readers = append(readers, f)
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()

	entries, stats, err := Merge(readers)
	if err != nil {
		log.Printf("warning: %v", err)
	}

	for _, e := range entries {
		fmt.Printf("[%s] %s %s: %s\n",
			e.Level, e.Timestamp.Format(time.RFC3339), e.Service, e.Message)
	}

	fmt.Printf("\nTotal: %d\n", stats.TotalEntries)
	fmt.Printf("First: %s\n", stats.FirstEntry.Format(time.RFC3339))
	fmt.Printf("Last:  %s\n", stats.LastEntry.Format(time.RFC3339))
	fmt.Println("By level:")
	for level, count := range stats.CountByLevel {
		fmt.Printf("  %s: %d\n", level, count)
	}
	fmt.Println("By service:")
	for svc, count := range stats.CountByService {
		fmt.Printf("  %s: %d\n", svc, count)
	}
}
