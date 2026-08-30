package app

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
)

type shortFrameWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *shortFrameWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit {
		data = data[:w.limit]
	}
	return w.buffer.Write(data)
}

func TestEnvelopeFrameRoundTripAndConcatenation(t *testing.T) {
	payloads := [][]byte{
		[]byte("a"),
		bytes.Repeat([]byte{2}, 128),
		[]byte("z"),
	}
	written := &shortFrameWriter{limit: 3}
	for _, payload := range payloads {
		if err := writeEnvelopeFrame(written, payload); err != nil {
			t.Fatal(err)
		}
	}

	reader := bufio.NewReader(bytes.NewReader(written.buffer.Bytes()))
	for index, want := range payloads {
		got, err := readEnvelopeFrame(reader, 128)
		if err != nil {
			t.Fatalf("read frame %d: %v", index, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d = %x, want %x", index, got, want)
		}
	}
	if _, err := readEnvelopeFrame(reader, 128); !errors.Is(err, io.EOF) {
		t.Fatalf("clean stream end = %v, want EOF", err)
	}
}

func TestEnvelopeFrameRejectsZeroLength(t *testing.T) {
	if err := writeEnvelopeFrame(io.Discard, nil); !errors.Is(err, errEnvelopeFrameZeroLength) {
		t.Fatalf("zero-length write error = %v", err)
	}
	reader := bufio.NewReader(bytes.NewReader([]byte{0}))
	if _, err := readEnvelopeFrame(reader, 10); !errors.Is(err, errEnvelopeFrameZeroLength) {
		t.Fatalf("zero-length read error = %v", err)
	}
}

func TestEnvelopeFrameRejectsOversizeBeforePayloadRead(t *testing.T) {
	prefix := binary.AppendUvarint(nil, 129)
	reader := &prefixOnlyReader{prefix: prefix}
	if _, err := readEnvelopeFrame(bufio.NewReader(reader), 128); !errors.Is(err, errEnvelopeTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	if reader.reads != 1 {
		t.Fatalf("oversize frame performed %d underlying reads, want prefix only", reader.reads)
	}

	huge := binary.AppendUvarint(nil, math.MaxUint64)
	reader = &prefixOnlyReader{prefix: huge}
	if _, err := readEnvelopeFrame(bufio.NewReader(reader), 128); !errors.Is(err, errEnvelopeTooLarge) {
		t.Fatalf("huge frame error = %v", err)
	}
	if reader.reads != 1 {
		t.Fatalf("huge frame performed %d underlying reads, want prefix only", reader.reads)
	}
}

type prefixOnlyReader struct {
	prefix []byte
	reads  int
}

func (r *prefixOnlyReader) Read(data []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, errors.New("payload read attempted")
	}
	return copy(data, r.prefix), nil
}

func TestEnvelopeFrameRejectsOverflowAndTruncation(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{name: "overflowed prefix", data: bytes.Repeat([]byte{0xff}, binary.MaxVarintLen64), want: errEnvelopeFrameLengthOverflow},
		{name: "truncated prefix", data: []byte{0x80}, want: errEnvelopeFrameTruncated},
		{name: "truncated payload", data: append(binary.AppendUvarint(nil, 3), 'a', 'b'), want: errEnvelopeFrameTruncated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := readEnvelopeFrame(bufio.NewReader(bytes.NewReader(test.data)), 10)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestEnvelopeFrameRejectsInvalidMaximum(t *testing.T) {
	for _, maximum := range []int{-1, 0} {
		_, err := readEnvelopeFrame(bufio.NewReader(bytes.NewReader([]byte{1, 'a'})), maximum)
		if !errors.Is(err, errInvalidEnvelopeFrameMaximum) {
			t.Fatalf("maximum %d error = %v", maximum, err)
		}
	}
}
