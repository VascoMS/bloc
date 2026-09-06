package app

import (
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
