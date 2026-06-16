package tui

import (
	"bytes"
	"strings"
)

type lineWriter struct {
	sink func(string)
	buf  bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		data := w.buf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(data[:i]), "\r")
		w.buf.Next(i + 1)
		if line != "" {
			w.sink(line)
		}
	}
	return len(p), nil
}
