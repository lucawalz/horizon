package tui

import (
	"strings"
	"testing"
)

func TestLineWriterSplitsPartialWrites(t *testing.T) {
	var got []string
	w := &lineWriter{sink: func(s string) { got = append(got, s) }}

	_, _ = w.Write([]byte("GET https://"))
	if len(got) != 0 {
		t.Fatalf("partial line should not flush yet, got %v", got)
	}
	_, _ = w.Write([]byte("api 200\nPATCH /x"))
	if len(got) != 1 || got[0] != "GET https://api 200" {
		t.Fatalf("first complete line = %v", got)
	}
	_, _ = w.Write([]byte(" 202\n"))
	if len(got) != 2 || got[1] != "PATCH /x 202" {
		t.Fatalf("second complete line = %v", got)
	}
	for _, line := range got {
		if strings.Contains(line, "\n") {
			t.Errorf("line should not contain newline: %q", line)
		}
	}
}

func TestLineWriterSkipsBlankLines(t *testing.T) {
	var got []string
	w := &lineWriter{sink: func(s string) { got = append(got, s) }}
	_, _ = w.Write([]byte("\n\nreal\n"))
	if len(got) != 1 || got[0] != "real" {
		t.Errorf("blank lines should be skipped, got %v", got)
	}
}
