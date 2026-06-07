package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"btd/curves"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"go.dedis.ch/kyber/v4"
	"go.dedis.ch/kyber/v4/pairing/bls12381/kilic"
)

// newSuite returns the pairing suite used by the current BTE integration.
func newSuite() curves.Suite {
	return curves.NewSuite(kilic.NewBLS12381Suite())
}

func marshalPointHex(p kyber.Point) (string, error) {
	b, err := p.MarshalBinary()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func unmarshalPointHex(suite curves.Suite, h string) (kyber.Point, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(h, "0x"))
	if err != nil {
		return nil, err
	}
	p := suite.G1().Point()
	if err := p.UnmarshalBinary(b); err != nil {
		return nil, err
	}
	return p, nil
}

func marshalScalarHex(s kyber.Scalar) (string, error) {
	b, err := s.MarshalBinary()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func unmarshalScalarHex(suite curves.Suite, h string) (kyber.Scalar, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(h, "0x"))
	if err != nil {
		return nil, err
	}
	s := suite.G1().Scalar()
	if err := s.UnmarshalBinary(b); err != nil {
		return nil, err
	}
	return s, nil
}

// hashHex returns a lowercase SHA-256 hex digest.
func hashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func validNonNegativeDecimal(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "0"
	}
	out, ok := new(big.Int).SetString(raw, 10)
	return ok && out.Sign() >= 0
}

// decodeHexMaybe accepts hex strings with or without 0x and falls back to raw
// bytes for non-hex input.
func decodeHexMaybe(s string) ([]byte, error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "0x"))
	if s == "" {
		return nil, fmt.Errorf("empty tx")
	}
	if len(s)%2 == 1 {
		s = "0" + s
	}
	if _, err := strconv.ParseUint(s[:1], 16, 8); err != nil {
		return []byte(s), nil
	}
	return hex.DecodeString(s)
}

// generateLibP2PIdentity creates the static Ed25519 identity stored in local
// cluster configs for libp2p experiments.
func generateLibP2PIdentity() (string, string, error) {
	priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return "", "", err
	}
	raw, err := libp2pcrypto.MarshalPrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	id, err := libp2ppeer.IDFromPrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(raw), id.String(), nil
}
