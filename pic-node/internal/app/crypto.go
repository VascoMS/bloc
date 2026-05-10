package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"btd/curves"
	"go.dedis.ch/kyber/v4"
	"go.dedis.ch/kyber/v4/pairing/bls12381/kilic"
)

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

func hashHex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

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
