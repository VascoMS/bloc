package app

import (
	"fmt"
	"io"

	"bloc-node/internal/pb/blocv1"
	"github.com/anthdm/hbbft"
	"google.golang.org/protobuf/proto"
)

const (
	envelopeVersion = 1
)

// EnvelopeCodec converts WireEnvelope values to transport bytes.
type EnvelopeCodec interface {
	Encode(WireEnvelope) ([]byte, error)
	Decode([]byte) (WireEnvelope, error)
}

// ProtoEnvelopeCodec serializes libp2p messages with generated protobuf
// bindings from proto/bloc/v1/messages.proto.
type ProtoEnvelopeCodec struct{}

// Encode writes a versioned protobuf envelope with an ACS or share payload.
func (ProtoEnvelopeCodec) Encode(env WireEnvelope) ([]byte, error) {
	msg := &blocv1.Envelope{
		Version: envelopeVersion,
		From:    env.From,
		To:      env.To,
		Direct:  env.Direct,
		Kind:    env.Kind,
		Slot:    env.Slot,
	}
	switch env.Kind {
	case "acs":
		if env.ACS == nil {
			return nil, fmt.Errorf("acs envelope has nil payload")
		}
		acs, err := toProtoSlotMessage(env.ACS)
		if err != nil {
			return nil, err
		}
		msg.Payload = &blocv1.Envelope_Acs{Acs: acs}
	case "share":
		if env.Share == nil {
			return nil, fmt.Errorf("share envelope has nil payload")
		}
		msg.Payload = &blocv1.Envelope_Share{Share: toProtoWireShare(*env.Share)}
	default:
		return nil, fmt.Errorf("unsupported envelope kind %q", env.Kind)
	}
	return proto.Marshal(msg)
}

// Decode validates the envelope version and converts the protobuf payload back
// to the local HoneyBadger/BTE message structs.
func (ProtoEnvelopeCodec) Decode(data []byte) (WireEnvelope, error) {
	var msg blocv1.Envelope
	if err := proto.Unmarshal(data, &msg); err != nil {
		return WireEnvelope{}, err
	}
	if msg.GetVersion() != envelopeVersion {
		return WireEnvelope{}, fmt.Errorf("unsupported envelope version %d", msg.GetVersion())
	}
	env := WireEnvelope{
		From:   msg.GetFrom(),
		To:     msg.GetTo(),
		Direct: msg.GetDirect(),
		Kind:   msg.GetKind(),
		Slot:   msg.GetSlot(),
	}
	switch payload := msg.GetPayload().(type) {
	case *blocv1.Envelope_Acs:
		acs, err := fromProtoSlotMessage(payload.Acs)
		if err != nil {
			return WireEnvelope{}, err
		}
		env.ACS = acs
	case *blocv1.Envelope_Share:
		share := fromProtoWireShare(payload.Share)
		env.Share = &share
	default:
		return WireEnvelope{}, io.ErrUnexpectedEOF
	}
	return env, nil
}

func toProtoWireShare(share WireShare) *blocv1.WireShare {
	return &blocv1.WireShare{
		OperatorId: uint64(share.OperatorID),
		BatchIdHex: share.BatchIDHex,
		SubBatchId: uint64(share.SubBatchID),
		PointHex:   share.PointHex,
	}
}

func fromProtoWireShare(share *blocv1.WireShare) WireShare {
	if share == nil {
		return WireShare{}
	}
	return WireShare{
		OperatorID: int(share.GetOperatorId()),
		BatchIDHex: share.GetBatchIdHex(),
		SubBatchID: int(share.GetSubBatchId()),
		PointHex:   share.GetPointHex(),
	}
}

func toProtoSlotMessage(msg *hbbft.SlotMessage) (*blocv1.SlotMessage, error) {
	acs, err := toProtoACSMessage(msg.Payload)
	if err != nil {
		return nil, err
	}
	return &blocv1.SlotMessage{Slot: msg.Slot, Payload: acs}, nil
}

func fromProtoSlotMessage(msg *blocv1.SlotMessage) (*hbbft.SlotMessage, error) {
	if msg == nil || msg.GetPayload() == nil {
		return nil, io.ErrUnexpectedEOF
	}
	acs, err := fromProtoACSMessage(msg.GetPayload())
	if err != nil {
		return nil, err
	}
	return &hbbft.SlotMessage{Slot: msg.GetSlot(), Payload: acs}, nil
}

func toProtoACSMessage(msg *hbbft.ACSMessage) (*blocv1.ACSMessage, error) {
	if msg == nil {
		return nil, fmt.Errorf("nil acs message")
	}
	out := &blocv1.ACSMessage{ProposerId: msg.ProposerID}
	switch payload := msg.Payload.(type) {
	case *hbbft.AgreementMessage:
		agreement, err := toProtoAgreementMessage(payload)
		if err != nil {
			return nil, err
		}
		out.Payload = &blocv1.ACSMessage_Agreement{Agreement: agreement}
	case *hbbft.BroadcastMessage:
		broadcast, err := toProtoBroadcastMessage(payload)
		if err != nil {
			return nil, err
		}
		out.Payload = &blocv1.ACSMessage_Broadcast{Broadcast: broadcast}
	default:
		return nil, fmt.Errorf("unsupported acs payload %T", msg.Payload)
	}
	return out, nil
}

func fromProtoACSMessage(msg *blocv1.ACSMessage) (*hbbft.ACSMessage, error) {
	if msg == nil {
		return nil, io.ErrUnexpectedEOF
	}
	out := &hbbft.ACSMessage{ProposerID: msg.GetProposerId()}
	switch payload := msg.GetPayload().(type) {
	case *blocv1.ACSMessage_Agreement:
		agreement, err := fromProtoAgreementMessage(payload.Agreement)
		if err != nil {
			return nil, err
		}
		out.Payload = agreement
	case *blocv1.ACSMessage_Broadcast:
		broadcast, err := fromProtoBroadcastMessage(payload.Broadcast)
		if err != nil {
			return nil, err
		}
		out.Payload = broadcast
	default:
		return nil, io.ErrUnexpectedEOF
	}
	return out, nil
}

func toProtoAgreementMessage(msg *hbbft.AgreementMessage) (*blocv1.AgreementMessage, error) {
	out := &blocv1.AgreementMessage{Epoch: uint64(msg.Epoch)}
	switch payload := msg.Message.(type) {
	case *hbbft.BvalRequest:
		out.Message = &blocv1.AgreementMessage_Bval{Bval: &blocv1.BvalRequest{Value: payload.Value}}
	case *hbbft.AuxRequest:
		out.Message = &blocv1.AgreementMessage_Aux{Aux: &blocv1.AuxRequest{Value: payload.Value}}
	default:
		return nil, fmt.Errorf("unsupported agreement payload %T", msg.Message)
	}
	return out, nil
}

func fromProtoAgreementMessage(msg *blocv1.AgreementMessage) (*hbbft.AgreementMessage, error) {
	if msg == nil {
		return nil, io.ErrUnexpectedEOF
	}
	out := &hbbft.AgreementMessage{Epoch: int(msg.GetEpoch())}
	switch payload := msg.GetMessage().(type) {
	case *blocv1.AgreementMessage_Bval:
		out.Message = &hbbft.BvalRequest{Value: payload.Bval.GetValue()}
	case *blocv1.AgreementMessage_Aux:
		out.Message = &hbbft.AuxRequest{Value: payload.Aux.GetValue()}
	default:
		return nil, io.ErrUnexpectedEOF
	}
	return out, nil
}

func toProtoBroadcastMessage(msg *hbbft.BroadcastMessage) (*blocv1.BroadcastMessage, error) {
	out := &blocv1.BroadcastMessage{}
	switch payload := msg.Payload.(type) {
	case *hbbft.ProofRequest:
		out.Payload = &blocv1.BroadcastMessage_Proof{Proof: toProtoProofRequest(payload)}
	case *hbbft.EchoRequest:
		out.Payload = &blocv1.BroadcastMessage_Echo{Echo: &blocv1.EchoRequest{Proof: toProtoProofRequest(&payload.ProofRequest)}}
	case *hbbft.ReadyRequest:
		out.Payload = &blocv1.BroadcastMessage_Ready{Ready: &blocv1.ReadyRequest{RootHash: payload.RootHash}}
	default:
		return nil, fmt.Errorf("unsupported broadcast payload %T", msg.Payload)
	}
	return out, nil
}

func fromProtoBroadcastMessage(msg *blocv1.BroadcastMessage) (*hbbft.BroadcastMessage, error) {
	if msg == nil {
		return nil, io.ErrUnexpectedEOF
	}
	out := &hbbft.BroadcastMessage{}
	switch payload := msg.GetPayload().(type) {
	case *blocv1.BroadcastMessage_Proof:
		out.Payload = fromProtoProofRequest(payload.Proof)
	case *blocv1.BroadcastMessage_Echo:
		proof := fromProtoProofRequest(payload.Echo.GetProof())
		out.Payload = &hbbft.EchoRequest{ProofRequest: *proof}
	case *blocv1.BroadcastMessage_Ready:
		out.Payload = &hbbft.ReadyRequest{RootHash: payload.Ready.GetRootHash()}
	default:
		return nil, io.ErrUnexpectedEOF
	}
	return out, nil
}

func toProtoProofRequest(req *hbbft.ProofRequest) *blocv1.ProofRequest {
	return &blocv1.ProofRequest{
		RootHash: req.RootHash,
		Proof:    req.Proof,
		Index:    uint64(req.Index),
		Leaves:   uint64(req.Leaves),
	}
}

func fromProtoProofRequest(req *blocv1.ProofRequest) *hbbft.ProofRequest {
	if req == nil {
		return &hbbft.ProofRequest{}
	}
	return &hbbft.ProofRequest{
		RootHash: req.GetRootHash(),
		Proof:    req.GetProof(),
		Index:    int(req.GetIndex()),
		Leaves:   int(req.GetLeaves()),
	}
}
