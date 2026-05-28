package app

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"

	"google.golang.org/protobuf/encoding/protowire"
)

const (
	envelopeVersion         = 1
	envelopePayloadEncoding = "gob-wire-envelope"
)

// EnvelopeCodec converts WireEnvelope values to transport bytes. Keeping this
// separate from transports lets TCP use legacy gob while libp2p uses a
// versioned envelope.
type EnvelopeCodec interface {
	Encode(WireEnvelope) ([]byte, error)
	Decode([]byte) (WireEnvelope, error)
}

// GobEnvelopeCodec is the compatibility codec used by the TCP transport.
type GobEnvelopeCodec struct{}

// Encode serializes an envelope using Go gob.
func (GobEnvelopeCodec) Encode(env WireEnvelope) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(env); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode deserializes a gob-encoded envelope.
func (GobEnvelopeCodec) Decode(data []byte) (WireEnvelope, error) {
	var env WireEnvelope
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&env); err != nil {
		return WireEnvelope{}, err
	}
	return env, nil
}

// ProtoEnvelopeCodec wraps the current protocol payload in a versioned
// protobuf-wire envelope. The inner payload remains gob-encoded until the ACS
// and BTE message types are promoted to explicit protobuf schemas.
type ProtoEnvelopeCodec struct{}

// Encode writes a protobuf-wire envelope with routing metadata and a gob
// payload adapter.
func (ProtoEnvelopeCodec) Encode(env WireEnvelope) ([]byte, error) {
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(env); err != nil {
		return nil, err
	}
	var out []byte
	out = protowire.AppendTag(out, 1, protowire.VarintType)
	out = protowire.AppendVarint(out, envelopeVersion)
	out = protowire.AppendTag(out, 2, protowire.VarintType)
	out = protowire.AppendVarint(out, env.From)
	out = protowire.AppendTag(out, 3, protowire.VarintType)
	out = protowire.AppendVarint(out, env.To)
	out = protowire.AppendTag(out, 4, protowire.VarintType)
	if env.Direct {
		out = protowire.AppendVarint(out, 1)
	} else {
		out = protowire.AppendVarint(out, 0)
	}
	out = protowire.AppendTag(out, 5, protowire.BytesType)
	out = protowire.AppendString(out, env.Kind)
	out = protowire.AppendTag(out, 6, protowire.VarintType)
	out = protowire.AppendVarint(out, env.Slot)
	out = protowire.AppendTag(out, 7, protowire.BytesType)
	out = protowire.AppendString(out, envelopePayloadEncoding)
	out = protowire.AppendTag(out, 8, protowire.BytesType)
	out = protowire.AppendBytes(out, payload.Bytes())
	return out, nil
}

// Decode validates the envelope version and decodes the embedded gob payload.
func (ProtoEnvelopeCodec) Decode(data []byte) (WireEnvelope, error) {
	var payload []byte
	var version uint64
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return WireEnvelope{}, protowire.ParseError(n)
		}
		data = data[n:]
		switch num {
		case 1:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return WireEnvelope{}, protowire.ParseError(n)
			}
			version = v
			data = data[n:]
		case 8:
			if typ != protowire.BytesType {
				return WireEnvelope{}, fmt.Errorf("payload has wire type %v", typ)
			}
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return WireEnvelope{}, protowire.ParseError(n)
			}
			payload = v
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				return WireEnvelope{}, protowire.ParseError(n)
			}
			data = data[n:]
		}
	}
	if version != envelopeVersion {
		return WireEnvelope{}, fmt.Errorf("unsupported envelope version %d", version)
	}
	if len(payload) == 0 {
		return WireEnvelope{}, io.ErrUnexpectedEOF
	}
	var env WireEnvelope
	if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&env); err != nil {
		return WireEnvelope{}, err
	}
	return env, nil
}
