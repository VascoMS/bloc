package app

import (
	"testing"

	"github.com/anthdm/hbbft"
)

func TestClassifyEnvelopeLane(t *testing.T) {
	tests := []struct {
		name string
		env  WireEnvelope
		want persistentStreamLane
	}{
		{name: "proof", env: laneACS(&hbbft.BroadcastMessage{Payload: &hbbft.ProofRequest{}}), want: persistentLaneData},
		{name: "echo", env: laneACS(&hbbft.BroadcastMessage{Payload: &hbbft.EchoRequest{}}), want: persistentLaneData},
		{name: "ready", env: laneACS(&hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{}}), want: persistentLaneControl},
		{name: "bval", env: laneACS(&hbbft.AgreementMessage{Message: &hbbft.BvalRequest{}}), want: persistentLaneControl},
		{name: "aux", env: laneACS(&hbbft.AgreementMessage{Message: &hbbft.AuxRequest{}}), want: persistentLaneControl},
		{name: "share", env: WireEnvelope{Kind: "share", Share: &WireShare{}}, want: persistentLaneData},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := classifyEnvelopeLane(test.env)
			if err != nil || got != test.want {
				t.Fatalf("lane = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestClassifyEnvelopeLaneRejectsInvalidEnvelope(t *testing.T) {
	for _, env := range []WireEnvelope{
		{},
		{Kind: "acs"},
		{Kind: "share"},
		{Kind: "acs", ACS: &hbbft.SlotMessage{}},
	} {
		if lane, err := classifyEnvelopeLane(env); err == nil {
			t.Fatalf("invalid envelope classified as %q: %+v", lane, env)
		}
	}
}

func laneACS(payload any) WireEnvelope {
	return WireEnvelope{Kind: "acs", ACS: slotACSMessage(payload)}
}
