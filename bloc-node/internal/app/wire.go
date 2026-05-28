package app

import (
	"encoding/gob"

	"github.com/anthdm/hbbft"
)

// registerGobTypes registers HoneyBadger concrete message types used inside
// interface fields. Gob decoding fails without these registrations.
func registerGobTypes() {
	gob.Register(&hbbft.SlotMessage{})
	gob.Register(&hbbft.ACSMessage{})
	gob.Register(&hbbft.BroadcastMessage{})
	gob.Register(&hbbft.ProofRequest{})
	gob.Register(&hbbft.EchoRequest{})
	gob.Register(&hbbft.ReadyRequest{})
	gob.Register(&hbbft.AgreementMessage{})
	gob.Register(&hbbft.BvalRequest{})
	gob.Register(&hbbft.AuxRequest{})
}
