package app

import (
	"errors"

	"github.com/anthdm/hbbft"
)

var errInvalidACSSubtype = errors.New("invalid ACS message subtype")

func classifyACSMessage(msg *hbbft.SlotMessage) (hbbft.ACSMessageSubtype, error) {
	if msg == nil || msg.Payload == nil || msg.Payload.Payload == nil {
		return "", errInvalidACSSubtype
	}
	switch payload := msg.Payload.Payload.(type) {
	case *hbbft.BroadcastMessage:
		if payload == nil {
			return "", errInvalidACSSubtype
		}
		switch nested := payload.Payload.(type) {
		case *hbbft.ProofRequest:
			if nested == nil {
				return "", errInvalidACSSubtype
			}
			return hbbft.ACSMessageProof, nil
		case *hbbft.EchoRequest:
			if nested == nil {
				return "", errInvalidACSSubtype
			}
			return hbbft.ACSMessageEcho, nil
		case *hbbft.ReadyRequest:
			if nested == nil {
				return "", errInvalidACSSubtype
			}
			return hbbft.ACSMessageReady, nil
		}
	case *hbbft.AgreementMessage:
		if payload == nil {
			return "", errInvalidACSSubtype
		}
		switch nested := payload.Message.(type) {
		case *hbbft.BvalRequest:
			if nested == nil {
				return "", errInvalidACSSubtype
			}
			return hbbft.ACSMessageBVAL, nil
		case *hbbft.AuxRequest:
			if nested == nil {
				return "", errInvalidACSSubtype
			}
			return hbbft.ACSMessageAUX, nil
		}
	}
	return "", errInvalidACSSubtype
}
