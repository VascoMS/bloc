package hbbft

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"sort"
)

// SlotMessage is the slot-scoped top-level message used by the BLOC adapter.
type SlotMessage struct {
	Slot    uint64
	Payload *ACSMessage
}

// CandidateBatchProvider abstracts how the surrounding BLOC pipeline obtains a
// candidate batch for a slot. The implementation can call HTTP, gRPC, or any
// other external service outside of the ACS path.
type CandidateBatchProvider interface {
	CandidateBatch(slot uint64) ([]byte, error)
}

// AcceptedBatch is a proposer-tagged candidate batch accepted by ACS.
type AcceptedBatch struct {
	ProposerID uint64
	Batch      []byte
}

// SlotBlockBody is the deterministic block-body representation produced from an
// agreed common subset.
type SlotBlockBody struct {
	Slot    uint64
	Batches []AcceptedBatch
}

// SlotOutput is the slot-scoped ACS result emitted to the surrounding BLOC
// pipeline.
type SlotOutput struct {
	Slot             uint64
	CommonSubset     map[uint64][]byte
	OrderedBatches   []AcceptedBatch
	BlockBody        []byte
	DecryptionResult interface{}
}

// BlockBuilder deterministically materializes a block body from the agreed
// common subset.
type BlockBuilder func(slot uint64, batches []AcceptedBatch) ([]byte, error)

// PostAgreementHook runs after ACS agreement and deterministic block assembly.
// It exists to keep decryption/materialization outside the ACS core.
type PostAgreementHook func(*SlotOutput) (interface{}, error)

// SlotConfig configures the slot-scoped BLOC adapter built on top of ACS.
type SlotConfig struct {
	Config
	Slot          uint64
	BlockBuilder  BlockBuilder
	PostAgreement PostAgreementHook
}

// SlotACS adapts ACS into a single-slot block-building primitive for BLOC.
type SlotACS struct {
	Config
	slot          uint64
	acs           *ACS
	blockBuilder  BlockBuilder
	postAgreement PostAgreementHook
	output        *SlotOutput
}

// NewSlotACS returns a slot-scoped adapter that reuses the existing ACS
// implementation without the epoch/mempool HoneyBadger driver.
func NewSlotACS(cfg SlotConfig) *SlotACS {
	return &SlotACS{
		Config:        cfg.Config,
		slot:          cfg.Slot,
		acs:           NewACS(cfg.Config),
		blockBuilder:  cfg.BlockBuilder,
		postAgreement: cfg.PostAgreement,
	}
}

// Slot returns the slot identifier handled by this adapter instance.
func (s *SlotACS) Slot() uint64 {
	return s.slot
}

// InputBatch injects the local candidate batch for this slot into ACS.
func (s *SlotACS) InputBatch(batch []byte) error {
	encodedBatch, err := encodeCandidateBatch(batch)
	if err != nil {
		return err
	}
	if err := s.acs.InputValue(encodedBatch); err != nil {
		return err
	}
	return s.maybeBuildOutput()
}

// InputFromProvider fetches the local candidate batch from an external
// provider and injects it into ACS.
func (s *SlotACS) InputFromProvider(provider CandidateBatchProvider) error {
	if provider == nil {
		return fmt.Errorf("candidate batch provider is nil")
	}
	batch, err := provider.CandidateBatch(s.slot)
	if err != nil {
		return err
	}
	return s.InputBatch(batch)
}

// HandleMessage processes a slot-scoped ACS message from another participant.
func (s *SlotACS) HandleMessage(senderID uint64, msg *SlotMessage) error {
	if msg == nil || msg.Payload == nil {
		return fmt.Errorf("received nil slot message")
	}
	if msg.Slot != s.slot {
		return fmt.Errorf("received message for slot %d, expected %d", msg.Slot, s.slot)
	}
	if err := s.acs.HandleMessage(senderID, msg.Payload); err != nil {
		return err
	}
	return s.maybeBuildOutput()
}

// Messages returns the queued slot-scoped network messages and drains the
// internal queue.
func (s *SlotACS) Messages() []MessageTuple {
	msgs := s.acs.messageQue.messages()
	out := make([]MessageTuple, len(msgs))
	for i, msg := range msgs {
		out[i] = MessageTuple{
			To:      msg.To,
			Payload: &SlotMessage{Slot: s.slot, Payload: msg.Payload.(*ACSMessage)},
		}
	}
	return out
}

// Output returns the slot result once. Subsequent calls return nil.
func (s *SlotACS) Output() *SlotOutput {
	if s.output == nil {
		return nil
	}
	out := s.output
	s.output = nil
	return out
}

func (s *SlotACS) maybeBuildOutput() error {
	if s.output != nil {
		return nil
	}
	subset := s.acs.Output()
	if len(subset) == 0 {
		return nil
	}
	decodedSubset, err := decodeCommonSubset(subset)
	if err != nil {
		return err
	}
	ordered := orderedBatches(decodedSubset)
	builder := s.blockBuilder
	if builder == nil {
		builder = EncodeSlotBlockBody
	}
	blockBody, err := builder(s.slot, ordered)
	if err != nil {
		return err
	}
	output := &SlotOutput{
		Slot:           s.slot,
		CommonSubset:   decodedSubset,
		OrderedBatches: ordered,
		BlockBody:      blockBody,
	}
	if s.postAgreement != nil {
		decryptionResult, err := s.postAgreement(output)
		if err != nil {
			return err
		}
		output.DecryptionResult = decryptionResult
	}
	s.output = output
	return nil
}

func decodeCommonSubset(subset map[uint64][]byte) (map[uint64][]byte, error) {
	decoded := make(map[uint64][]byte, len(subset))
	for proposerID, batch := range subset {
		plainBatch, err := decodeCandidateBatch(batch)
		if err != nil {
			return nil, fmt.Errorf("decode proposer %d batch: %w", proposerID, err)
		}
		decoded[proposerID] = plainBatch
	}
	return decoded, nil
}

func orderedBatches(subset map[uint64][]byte) []AcceptedBatch {
	ids := make([]uint64, 0, len(subset))
	for id := range subset {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	ordered := make([]AcceptedBatch, len(ids))
	for i, id := range ids {
		ordered[i] = AcceptedBatch{
			ProposerID: id,
			Batch:      append([]byte(nil), subset[id]...),
		}
	}
	return ordered
}

func encodeCandidateBatch(batch []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(batch); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeCandidateBatch(batch []byte) ([]byte, error) {
	var decoded []byte
	if err := gob.NewDecoder(bytes.NewReader(batch)).Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// EncodeSlotBlockBody is the default deterministic block builder. It preserves
// the proposer order by sorting accepted batches by proposer id and gob-encodes
// the resulting block body.
func EncodeSlotBlockBody(slot uint64, batches []AcceptedBatch) ([]byte, error) {
	body := SlotBlockBody{
		Slot:    slot,
		Batches: batches,
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
