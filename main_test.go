package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseLine_Valid(t *testing.T) {
	line := `2024-01-15T10:04:01Z ERROR service=auth message="invalid token"`
	entry, err := parseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Level != "ERROR" {
		t.Errorf("level = %q, want ERROR", entry.Level)
	}
	if entry.Service != "auth" {
		t.Errorf("service = %q, want auth", entry.Service)
	}
	if entry.Message != "invalid token" {
		t.Errorf("message = %q, want \"invalid token\"", entry.Message)
	}
	if !entry.Timestamp.Equal(mustParseTime("2024-01-15T10:04:01Z")) {
		t.Errorf("timestamp = %v, want 2024-01-15T10:04:01Z", entry.Timestamp)
	}
}

func TestParseLine_EmptyMessage(t *testing.T) {
	line := `2024-01-15T10:04:01Z INFO service=api message=""`
	entry, err := parseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Message != "" {
		t.Errorf("message = %q, want empty", entry.Message)
	}
}

func TestParseLine_Malformed(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"empty", ""},
		{"no space", "2024-01-15T10:04:01Z"},
		{"bad timestamp", "not-a-date INFO service=x message=\"y\""},
		{"missing service", `2024-01-15T10:04:01Z ERROR message="oops"`},
		{"no level", "2024-01-15T10:04:01Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseLine(tc.line)
			if err == nil {
				t.Errorf("expected error for line %q", tc.line)
			}
		})
	}
}

func TestMerge_BasicThreeSources(t *testing.T) {
	r1 := strings.NewReader(`2024-01-15T10:04:01Z ERROR service=auth message="invalid token"
2024-01-15T10:04:05Z INFO service=auth message="login success"`)

	r2 := strings.NewReader(`2024-01-15T10:04:03Z INFO service=api message="request received"
2024-01-15T10:04:03Z WARN service=db message="slow query detected"`)

	r3 := strings.NewReader(`2024-01-15T10:04:02Z INFO service=gateway message="routing request"
2024-01-15T10:04:06Z ERROR service=gateway message="upstream timeout"`)

	entries, stats, err := Merge([]io.Reader{r1, r2, r3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.TotalEntries != 6 {
		t.Errorf("TotalEntries = %d, want 6", stats.TotalEntries)
	}

	// Verify chronological order
	for i := 1; i < len(entries); i++ {
		prev, cur := entries[i-1], entries[i]
		if cur.Timestamp.Before(prev.Timestamp) {
			t.Errorf("entry %d (%v) is before entry %d (%v)",
				i, cur.Timestamp, i-1, prev.Timestamp)
		}
	}

	// Verify tie-breaking: at 10:04:03Z, api < db alphabetically
	if entries[2].Service != "api" || entries[3].Service != "db" {
		t.Errorf("tie-breaking failed: got [%s, %s], want [api, db]",
			entries[2].Service, entries[3].Service)
	}

	// Verify stats
	if stats.CountByLevel["ERROR"] != 2 {
		t.Errorf("ERROR count = %d, want 2", stats.CountByLevel["ERROR"])
	}
	if stats.CountByLevel["INFO"] != 3 {
		t.Errorf("INFO count = %d, want 3", stats.CountByLevel["INFO"])
	}
	if stats.CountByLevel["WARN"] != 1 {
		t.Errorf("WARN count = %d, want 1", stats.CountByLevel["WARN"])
	}
	if stats.CountByService["auth"] != 2 {
		t.Errorf("auth count = %d, want 2", stats.CountByService["auth"])
	}

	if !stats.FirstEntry.Equal(mustParseTime("2024-01-15T10:04:01Z")) {
		t.Errorf("FirstEntry = %v, want 2024-01-15T10:04:01Z", stats.FirstEntry)
	}
	if !stats.LastEntry.Equal(mustParseTime("2024-01-15T10:04:06Z")) {
		t.Errorf("LastEntry = %v, want 2024-01-15T10:04:06Z", stats.LastEntry)
	}
}

func TestMerge_EmptyReader(t *testing.T) {
	r1 := strings.NewReader("")
	r2 := strings.NewReader(`2024-01-15T10:04:01Z INFO service=api message="hello"`)

	entries, stats, err := Merge([]io.Reader{r1, r2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TotalEntries != 1 {
		t.Errorf("TotalEntries = %d, want 1", stats.TotalEntries)
	}
	if len(entries) != 1 {
		t.Errorf("len(entries) = %d, want 1", len(entries))
	}
}

func TestMerge_NoSources(t *testing.T) {
	entries, stats, err := Merge([]io.Reader{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TotalEntries != 0 {
		t.Errorf("TotalEntries = %d, want 0", stats.TotalEntries)
	}
	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0", len(entries))
	}
}

func TestMerge_SingleReader(t *testing.T) {
	r := strings.NewReader(`2024-01-15T10:04:03Z INFO service=api message="second"
2024-01-15T10:04:01Z ERROR service=auth message="first"`)

	entries, _, err := Merge([]io.Reader{r})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be sorted even if the file was out of order
	if entries[0].Service != "auth" {
		t.Errorf("first entry service = %q, want auth", entries[0].Service)
	}
	if entries[1].Service != "api" {
		t.Errorf("second entry service = %q, want api", entries[1].Service)
	}
}

func TestMerge_MalformedLinesSkipped(t *testing.T) {
	r := strings.NewReader(`2024-01-15T10:04:01Z INFO service=api message="good"
this is garbage
also bad
2024-01-15T10:04:02Z WARN service=db message="also good"`)

	entries, stats, err := Merge([]io.Reader{r})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TotalEntries != 2 {
		t.Errorf("TotalEntries = %d, want 2", stats.TotalEntries)
	}
	if len(entries) != 2 {
		t.Errorf("len(entries) = %d, want 2", len(entries))
	}
}

func TestMerge_DuplicateEntries(t *testing.T) {
	r1 := strings.NewReader(`2024-01-15T10:04:01Z INFO service=api message="hello"`)
	r2 := strings.NewReader(`2024-01-15T10:04:01Z INFO service=api message="hello"`)

	entries, stats, err := Merge([]io.Reader{r1, r2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TotalEntries != 2 {
		t.Errorf("TotalEntries = %d, want 2 (duplicates kept)", stats.TotalEntries)
	}
	if len(entries) != 2 {
		t.Errorf("len(entries) = %d, want 2", len(entries))
	}
}

func TestMerge_ManySources(t *testing.T) {
	// 10 readers to stress concurrency
	var readers []io.Reader
	for i := 0; i < 10; i++ {
		readers = append(readers, strings.NewReader(
			`2024-01-15T10:04:01Z INFO service=svc message="entry"
2024-01-15T10:04:02Z WARN service=svc message="entry"
2024-01-15T10:04:03Z ERROR service=svc message="entry"`))
	}

	entries, stats, err := Merge(readers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TotalEntries != 30 {
		t.Errorf("TotalEntries = %d, want 30", stats.TotalEntries)
	}
	if len(entries) != 30 {
		t.Errorf("len(entries) = %d, want 30", len(entries))
	}

	// Verify sorted
	for i := 1; i < len(entries); i++ {
		if entries[i].Timestamp.Before(entries[i-1].Timestamp) {
			t.Errorf("not sorted at index %d", i)
			break
		}
	}
}

func TestMerge_SameTimestampTieBreaking(t *testing.T) {
	// All entries at same time, different services
	r := strings.NewReader(`2024-01-15T10:04:01Z INFO service=zebra message="z"
2024-01-15T10:04:01Z INFO service=alpha message="a"
2024-01-15T10:04:01Z INFO service=middle message="m"`)

	entries, _, err := Merge([]io.Reader{r})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"alpha", "middle", "zebra"}
	for i, want := range expected {
		if entries[i].Service != want {
			t.Errorf("entries[%d].Service = %q, want %q", i, entries[i].Service, want)
		}
	}
}

func TestMerge_MessageWithSpaces(t *testing.T) {
	r := strings.NewReader(`2024-01-15T10:04:01Z INFO service=api message="hello world foo bar baz"`)

	entries, _, err := Merge([]io.Reader{r})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries[0].Message != "hello world foo bar baz" {
		t.Errorf("message = %q, want \"hello world foo bar baz\"", entries[0].Message)
	}
}

func TestMergeWithFilter_ErrorsOnly(t *testing.T) {
	r1 := strings.NewReader(`2024-01-15T10:04:01Z ERROR service=auth message="bad token"
2024-01-15T10:04:02Z INFO service=auth message="ok"`)
	r2 := strings.NewReader(`2024-01-15T10:04:03Z WARN service=db message="slow"
2024-01-15T10:04:04Z ERROR service=api message="500"`)

	entries, stats, err := MergeWithFilter([]io.Reader{r1, r2}, func(e LogEntry) bool {
		return e.Level == "ERROR"
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TotalEntries != 2 {
		t.Errorf("TotalEntries = %d, want 2", stats.TotalEntries)
	}
	if len(entries) != 2 {
		t.Errorf("len(entries) = %d, want 2", len(entries))
	}
	for _, e := range entries {
		if e.Level != "ERROR" {
			t.Errorf("expected only ERROR entries, got %s", e.Level)
		}
	}
}

func TestMergeWithFilter_NilFilter(t *testing.T) {
	r := strings.NewReader(`2024-01-15T10:04:01Z INFO service=api message="hello"`)

	entries, _, err := MergeWithFilter([]io.Reader{r}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("len(entries) = %d, want 1", len(entries))
	}
}

// errReader is an io.Reader that returns some valid data then fails with an error.
type errReader struct {
	data string
	read bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, errors.New("disk read failure")
}

func TestMerge_ReaderError(t *testing.T) {
	// One good reader, one that fails mid-read
	good := strings.NewReader(`2024-01-15T10:04:01Z INFO service=api message="hello"`)
	bad := &errReader{data: "2024-01-15T10:04:02Z ERROR service=auth message=\"fail\"\n"}

	entries, stats, err := Merge([]io.Reader{good, bad})

	// Error should be propagated
	if err == nil {
		t.Fatal("expected error from failing reader, got nil")
	}
	if !strings.Contains(err.Error(), "disk read failure") {
		t.Errorf("error = %q, want it to contain 'disk read failure'", err.Error())
	}

	// Valid entries from both readers should still be returned
	if len(entries) < 1 {
		t.Errorf("expected at least 1 entry, got %d", len(entries))
	}

	// Stats should reflect whatever entries were successfully parsed
	if stats.TotalEntries != len(entries) {
		t.Errorf("TotalEntries = %d, want %d", stats.TotalEntries, len(entries))
	}
}

func TestMerge_AllReadersError(t *testing.T) {
	bad1 := &errReader{data: ""}
	bad2 := &errReader{data: ""}

	entries, stats, err := Merge([]io.Reader{bad1, bad2})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
	if stats.TotalEntries != 0 {
		t.Errorf("TotalEntries = %d, want 0", stats.TotalEntries)
	}
}

func TestMergeWithFilter_ServiceFilter(t *testing.T) {
	r := strings.NewReader(`2024-01-15T10:04:01Z INFO service=api message="one"
2024-01-15T10:04:02Z INFO service=auth message="two"
2024-01-15T10:04:03Z INFO service=api message="three"`)

	entries, stats, err := MergeWithFilter([]io.Reader{r}, func(e LogEntry) bool {
		return e.Service == "api"
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TotalEntries != 2 {
		t.Errorf("TotalEntries = %d, want 2", stats.TotalEntries)
	}
	if len(entries) != 2 {
		t.Errorf("len(entries) = %d, want 2", len(entries))
	}
}
