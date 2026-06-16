package tui

import (
	"bytes"
	"flag"
	"io"
	"strings"

	"k8s.io/klog/v2"
)

const apiTraceVerbosity = "6"

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

func enableAPITrace(sink func(string)) (restore func()) {
	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	_ = fs.Set("logtostderr", "false")
	_ = fs.Set("alsologtostderr", "false")
	_ = fs.Set("stderrthreshold", "FATAL")
	_ = fs.Set("v", apiTraceVerbosity)
	klog.SetOutput(&lineWriter{sink: sink})
	return func() {
		rfs := flag.NewFlagSet("klog", flag.ContinueOnError)
		klog.InitFlags(rfs)
		_ = rfs.Set("logtostderr", "false")
		_ = rfs.Set("alsologtostderr", "false")
		_ = rfs.Set("stderrthreshold", "FATAL")
		_ = rfs.Set("v", "0")
		klog.SetOutput(io.Discard)
	}
}
