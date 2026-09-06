package app

import (
	"context"
	"fmt"

	"github.com/anthdm/hbbft"
	protocol "github.com/libp2p/go-libp2p/core/protocol"
)

type persistentStreamLane string

const (
	persistentLaneControl persistentStreamLane = "control"
	persistentLaneData    persistentStreamLane = "data"

	blocEnvelopeProtocolControl = protocol.ID("/bloc/envelope/3.0.0/control")
	blocEnvelopeProtocolData    = protocol.ID("/bloc/envelope/3.0.0/data")
)

var persistentStreamLanes = [...]persistentStreamLane{
	persistentLaneControl,
	persistentLaneData,
}

type persistentLaneStreamOpener func(context.Context, uint64, persistentStreamLane) (persistentWriteStream, error)

type peerStreamLaneWriters struct {
	control *peerStreamWriter
	data    *peerStreamWriter
}

func newPeerStreamLaneWriters(operatorID uint64, open persistentLaneStreamOpener, stop <-chan struct{}) *peerStreamLaneWriters {
	newWriter := func(lane persistentStreamLane) *peerStreamWriter {
		return newPeerStreamWriter(operatorID, func(ctx context.Context, to uint64) (persistentWriteStream, error) {
			return open(ctx, to, lane)
		}, stop)
	}
	return &peerStreamLaneWriters{
		control: newWriter(persistentLaneControl),
		data:    newWriter(persistentLaneData),
	}
}

func (w *peerStreamLaneWriters) writer(lane persistentStreamLane) (*peerStreamWriter, error) {
	if w == nil {
		return nil, fmt.Errorf("persistent lane writers are nil")
	}
	switch lane {
	case persistentLaneControl:
		return w.control, nil
	case persistentLaneData:
		return w.data, nil
	default:
		return nil, fmt.Errorf("unknown persistent stream lane %q", lane)
	}
}

func persistentLaneProtocol(lane persistentStreamLane) (protocol.ID, error) {
	switch lane {
	case persistentLaneControl:
		return blocEnvelopeProtocolControl, nil
	case persistentLaneData:
		return blocEnvelopeProtocolData, nil
	default:
		return "", fmt.Errorf("unknown persistent stream lane %q", lane)
	}
}

func classifyEnvelopeLane(env WireEnvelope) (persistentStreamLane, error) {
	if err := validateEnvelopePayload(env); err != nil {
		return "", err
	}
	if env.Kind == "share" {
		return persistentLaneData, nil
	}
	subtype, err := classifyACSMessage(env.ACS)
	if err != nil {
		return "", err
	}
	switch subtype {
	case hbbft.ACSMessageReady, hbbft.ACSMessageBVAL, hbbft.ACSMessageAUX:
		return persistentLaneControl, nil
	case hbbft.ACSMessageProof, hbbft.ACSMessageEcho:
		return persistentLaneData, nil
	default:
		return "", fmt.Errorf("unmapped ACS message subtype %q", subtype)
	}
}
