package app

import (
	"testing"

	"github.com/anthdm/hbbft"
)

func TestClassifyACSMessage(t *testing.T) {
	var nilProof *hbbft.ProofRequest
	var nilBVAL *hbbft.BvalRequest
	tests := []struct {
		name    string
		message *hbbft.SlotMessage
		want    hbbft.ACSMessageSubtype
		wantErr bool
	}{
		{
			name: "proof",
			message: slotACSMessage(&hbbft.BroadcastMessage{
				Payload: &hbbft.ProofRequest{},
			}),
			want: hbbft.ACSMessageProof,
		},
		{
			name: "echo",
			message: slotACSMessage(&hbbft.BroadcastMessage{
				Payload: &hbbft.EchoRequest{},
			}),
			want: hbbft.ACSMessageEcho,
		},
		{
			name: "ready",
			message: slotACSMessage(&hbbft.BroadcastMessage{
				Payload: &hbbft.ReadyRequest{},
			}),
			want: hbbft.ACSMessageReady,
		},
		{
			name: "bval",
			message: slotACSMessage(&hbbft.AgreementMessage{
				Message: &hbbft.BvalRequest{},
			}),
			want: hbbft.ACSMessageBVAL,
		},
		{
			name: "aux",
			message: slotACSMessage(&hbbft.AgreementMessage{
				Message: &hbbft.AuxRequest{},
			}),
			want: hbbft.ACSMessageAUX,
		},
		{name: "nil slot message", wantErr: true},
		{name: "nil ACS payload", message: &hbbft.SlotMessage{}, wantErr: true},
		{
			name:    "nil nested payload",
			message: &hbbft.SlotMessage{Payload: &hbbft.ACSMessage{}},
			wantErr: true,
		},
		{
			name:    "unknown nested payload",
			message: slotACSMessage(struct{}{}),
			wantErr: true,
		},
		{
			name:    "typed nil broadcast payload",
			message: slotACSMessage(&hbbft.BroadcastMessage{Payload: nilProof}),
			wantErr: true,
		},
		{
			name:    "typed nil agreement payload",
			message: slotACSMessage(&hbbft.AgreementMessage{Message: nilBVAL}),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := classifyACSMessage(test.message)
			if test.wantErr {
				if err == nil {
					t.Fatalf("classified invalid message as %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("subtype = %q, want %q", got, test.want)
			}
		})
	}
}

func slotACSMessage(payload any) *hbbft.SlotMessage {
	return &hbbft.SlotMessage{
		Slot:    1,
		Payload: &hbbft.ACSMessage{Payload: payload},
	}
}
