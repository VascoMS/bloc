package app

import (
	"bytes"
	"testing"
)

type shortWriter struct {
	bytes.Buffer
	limit int
}

func (w *shortWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit {
		data = data[:w.limit]
	}
	return w.Buffer.Write(data)
}

func TestWriteAllCompletesShortWrites(t *testing.T) {
	payload := bytes.Repeat([]byte("bloc-envelope"), 8192)
	writer := &shortWriter{limit: 37}
	if err := writeAll(writer, payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(writer.Bytes(), payload) {
		t.Fatalf("wrote %d bytes, want %d", writer.Len(), len(payload))
	}
}
