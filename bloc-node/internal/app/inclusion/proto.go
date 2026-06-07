package inclusion

import (
	"fmt"
	"io"

	"bloc-node/internal/pb/blocv1"
	"google.golang.org/protobuf/proto"
)

// EncodeList serializes an inclusion list with the generated BLOC protobuf
// schema used as the ACS proposal payload.
func EncodeList(list InclusionList) ([]byte, error) {
	msg := &blocv1.InclusionList{
		Slot:       list.Slot,
		OperatorId: list.OperatorID,
		Items:      make([]*blocv1.EncryptedPlaceholder, 0, len(list.Items)),
	}
	for _, item := range list.Items {
		msg.Items = append(msg.Items, toProtoEncryptedPlaceholder(item))
	}
	return proto.Marshal(msg)
}

// DecodeList parses an ACS proposal payload and recomputes its stable hash.
func DecodeList(data []byte) (InclusionList, error) {
	var msg blocv1.InclusionList
	if err := proto.Unmarshal(data, &msg); err != nil {
		return InclusionList{}, err
	}
	if msg.GetSlot() == 0 {
		return InclusionList{}, io.ErrUnexpectedEOF
	}
	list := InclusionList{
		Slot:       msg.GetSlot(),
		OperatorID: msg.GetOperatorId(),
		Items:      make([]EncryptedPlaceholder, 0, len(msg.GetItems())),
	}
	for _, item := range msg.GetItems() {
		placeholder, err := fromProtoEncryptedPlaceholder(item)
		if err != nil {
			return InclusionList{}, err
		}
		list.Items = append(list.Items, placeholder)
	}
	list.Hash = HashInclusionList(list)
	return list, nil
}

func toProtoEncryptedPlaceholder(item EncryptedPlaceholder) *blocv1.EncryptedPlaceholder {
	return &blocv1.EncryptedPlaceholder{
		Hash:                  item.Hash,
		Ciphertext:            item.Ciphertext,
		Gas:                   item.Gas,
		EffectiveFeePerGasWei: item.EffectiveFeePerGasWei,
		From:                  item.From,
		Nonce:                 item.Nonce,
		Kind:                  item.Kind,
	}
}

func fromProtoEncryptedPlaceholder(item *blocv1.EncryptedPlaceholder) (EncryptedPlaceholder, error) {
	if item == nil || len(item.GetCiphertext()) == 0 {
		return EncryptedPlaceholder{}, fmt.Errorf("placeholder has empty ciphertext")
	}
	return EncryptedPlaceholder{
		Hash:                  item.GetHash(),
		Ciphertext:            item.GetCiphertext(),
		Gas:                   item.GetGas(),
		EffectiveFeePerGasWei: item.GetEffectiveFeePerGasWei(),
		From:                  item.GetFrom(),
		Nonce:                 item.GetNonce(),
		Kind:                  item.GetKind(),
	}, nil
}
