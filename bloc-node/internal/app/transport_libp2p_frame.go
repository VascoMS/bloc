package app

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	errEnvelopeFrameZeroLength     = errors.New("envelope frame has zero length")
	errEnvelopeFrameLengthOverflow = errors.New("envelope frame length overflows uint64")
	errEnvelopeFrameTruncated      = errors.New("envelope frame is truncated")
	errInvalidEnvelopeFrameMaximum = errors.New("invalid maximum envelope frame size")
)

func writeEnvelopeFrame(writer io.Writer, payload []byte) error {
	if len(payload) == 0 {
		return errEnvelopeFrameZeroLength
	}
	var prefix [binary.MaxVarintLen64]byte
	prefixLength := binary.PutUvarint(prefix[:], uint64(len(payload)))
	if err := writeAll(writer, prefix[:prefixLength]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func readEnvelopeFrame(reader *bufio.Reader, maxEnvelopeBytes int) ([]byte, error) {
	if maxEnvelopeBytes < 1 {
		return nil, fmt.Errorf("%w: %d", errInvalidEnvelopeFrameMaximum, maxEnvelopeBytes)
	}
	length, err := binary.ReadUvarint(reader)
	if err != nil {
		switch {
		case errors.Is(err, io.EOF):
			return nil, io.EOF
		case errors.Is(err, io.ErrUnexpectedEOF):
			return nil, fmt.Errorf("%w: length prefix", errEnvelopeFrameTruncated)
		default:
			return nil, fmt.Errorf("%w: %v", errEnvelopeFrameLengthOverflow, err)
		}
	}
	if length == 0 {
		return nil, errEnvelopeFrameZeroLength
	}
	if length > uint64(maxEnvelopeBytes) {
		return nil, fmt.Errorf("%w: encoded %d bytes, maximum %d", errEnvelopeTooLarge, length, maxEnvelopeBytes)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("%w: payload: %v", errEnvelopeFrameTruncated, err)
	}
	return payload, nil
}
