package prf

import (
	"btd/curves"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"go.dedis.ch/kyber/v4"
	"io"
	"sync"
)

const (
	publicCRSMagic   = "BLOCPRF\x00"
	publicCRSVersion = uint32(1)
	maxCRSPointBytes = 1 << 20
)

type mkey struct {
	i int
	j int
}

type PRF struct {
	xi     []kyber.Scalar
	zi     []kyber.Scalar
	g2zi   []kyber.Point
	gTzi   []kyber.Point
	G1xi   []kyber.Point
	g2zixj map[mkey]kyber.Point
	B      int
	suite  curves.Suite
}

func PRFSetup(suite curves.Suite, B int, parallel bool) *PRF {
	setup := &PRF{
		xi:     make([]kyber.Scalar, B),
		zi:     make([]kyber.Scalar, B),
		g2zi:   make([]kyber.Point, B),
		gTzi:   make([]kyber.Point, B),
		G1xi:   make([]kyber.Point, B),
		g2zixj: make(map[mkey]kyber.Point),
		B:      B,
		suite:  suite,
	}
	for i := 0; i < B; i++ {
		setup.xi[i] = suite.G1().Scalar().Pick(suite.RandomStream())
		setup.zi[i] = suite.G2().Scalar().Pick(suite.RandomStream())
		setup.G1xi[i] = suite.G1().Point().Mul(setup.xi[i], suite.G1().Point().Base())
		setup.g2zi[i] = suite.G2().Point().Mul(setup.zi[i], suite.G2().Point().Base())
		setup.gTzi[i] = suite.GT().Point().Mul(setup.zi[i], suite.GTBase())
	}
	if !parallel {
		for i := 0; i < B; i++ {
			for j := 0; j < B; j++ { // Note that in the REAL setup, one does not compute and publish the values
				// for j == i!!! Doing so would make it insecure. We just include them here for testing purposes.
				setup.g2zixj[mkey{
					i: i,
					j: j,
				}] = suite.G2().Point().Mul(suite.G2().Scalar().Div(setup.zi[i], setup.xi[j]), suite.G2().Point().Base())
			}
		}
		return setup
	}
	// parallelized version below for faster setup generation...
	wg := sync.WaitGroup{}
	const PAR = 16
	wg.Add(PAR)
	buffer := make([][]struct {
		mkey
		kyber.Point
	}, PAR)
	for p := 0; p < PAR; p++ {
		start := p * (B / PAR)
		end := (p + 1) * (B / PAR)
		if p == PAR-1 {
			end = B
		}
		go func(instance, start, end int) {
			buffer[instance] = make([]struct {
				mkey
				kyber.Point
			}, B*(end-start))
			for i := start; i < end; i++ {
				for j := 0; j < B; j++ {
					buffer[instance][(i-start)*B+j].mkey = mkey{
						i: i,
						j: j,
					}
					buffer[instance][(i-start)*B+j].Point = suite.G2().Point().Mul(suite.G2().Scalar().Div(setup.zi[i], setup.xi[j]), suite.G2().Point().Base())
				}
			}
			wg.Done()
		}(p, start, end)
	}
	wg.Wait()
	for i := 0; i < PAR; i++ {
		for _, elem := range buffer[i] {
			setup.g2zixj[elem.mkey] = elem.Point
		}
	}
	return setup
}

func PRFSetupFromSeed(suite curves.Suite, B int, seed []byte) *PRF {
	setup := &PRF{
		xi:     make([]kyber.Scalar, B),
		zi:     make([]kyber.Scalar, B),
		g2zi:   make([]kyber.Point, B),
		gTzi:   make([]kyber.Point, B),
		G1xi:   make([]kyber.Point, B),
		g2zixj: make(map[mkey]kyber.Point),
		B:      B,
		suite:  suite,
	}
	for i := 0; i < B; i++ {
		setup.xi[i] = scalarFromSeed(suite.G1().Scalar(), seed, "xi", i)
		setup.zi[i] = scalarFromSeed(suite.G2().Scalar(), seed, "zi", i)
		setup.G1xi[i] = suite.G1().Point().Mul(setup.xi[i], suite.G1().Point().Base())
		setup.g2zi[i] = suite.G2().Point().Mul(setup.zi[i], suite.G2().Point().Base())
		setup.gTzi[i] = suite.GT().Point().Mul(setup.zi[i], suite.GTBase())
	}
	for i := 0; i < B; i++ {
		for j := 0; j < B; j++ {
			setup.g2zixj[mkey{i: i, j: j}] = suite.G2().Point().Mul(suite.G2().Scalar().Div(setup.zi[i], setup.xi[j]), suite.G2().Point().Base())
		}
	}
	return setup
}

// MarshalPublicBinary serializes the reusable public PRF parameters without
// the setup seed or the xi/zi scalars used to generate them. Cross-index
// elements are written in deterministic row-major order.
func (f *PRF) MarshalPublicBinary(suiteID string) ([]byte, error) {
	if f == nil || f.B <= 0 {
		return nil, fmt.Errorf("invalid PRF setup")
	}
	var out bytes.Buffer
	out.WriteString(publicCRSMagic)
	if err := binary.Write(&out, binary.BigEndian, publicCRSVersion); err != nil {
		return nil, err
	}
	if err := writeCRSBytes(&out, []byte(suiteID)); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.BigEndian, uint32(f.B)); err != nil {
		return nil, err
	}
	for _, point := range f.G1xi {
		if err := writeCRSPoint(&out, point); err != nil {
			return nil, err
		}
	}
	for _, point := range f.g2zi {
		if err := writeCRSPoint(&out, point); err != nil {
			return nil, err
		}
	}
	for i := 0; i < f.B; i++ {
		for j := 0; j < f.B; j++ {
			point, ok := f.g2zixj[mkey{i: i, j: j}]
			if !ok || point == nil {
				return nil, fmt.Errorf("missing public CRS point (%d,%d)", i, j)
			}
			if err := writeCRSPoint(&out, point); err != nil {
				return nil, err
			}
		}
	}
	return out.Bytes(), nil
}

// PRFSetupFromPublic reconstructs runtime PRF state from serialized public
// parameters. It intentionally leaves xi and zi unset because production
// encryption and decryption do not require the setup trapdoors.
func PRFSetupFromPublic(suite curves.Suite, expectedSuiteID string, expectedB int, encoded []byte) (*PRF, error) {
	reader := bytes.NewReader(encoded)
	magic := make([]byte, len(publicCRSMagic))
	if _, err := io.ReadFull(reader, magic); err != nil {
		return nil, fmt.Errorf("read public CRS magic: %w", err)
	}
	if string(magic) != publicCRSMagic {
		return nil, fmt.Errorf("invalid public CRS magic")
	}
	var version uint32
	if err := binary.Read(reader, binary.BigEndian, &version); err != nil {
		return nil, fmt.Errorf("read public CRS version: %w", err)
	}
	if version != publicCRSVersion {
		return nil, fmt.Errorf("unsupported public CRS version %d", version)
	}
	suiteID, err := readCRSBytes(reader)
	if err != nil {
		return nil, fmt.Errorf("read public CRS suite: %w", err)
	}
	if string(suiteID) != expectedSuiteID {
		return nil, fmt.Errorf("public CRS suite %q does not match expected suite %q", suiteID, expectedSuiteID)
	}
	var domain uint32
	if err := binary.Read(reader, binary.BigEndian, &domain); err != nil {
		return nil, fmt.Errorf("read public CRS domain: %w", err)
	}
	if expectedB <= 0 || uint64(expectedB) > uint64(^uint32(0)) || domain != uint32(expectedB) {
		return nil, fmt.Errorf("public CRS domain %d does not match expected BMax %d", domain, expectedB)
	}
	setup := &PRF{
		G1xi:   make([]kyber.Point, expectedB),
		g2zi:   make([]kyber.Point, expectedB),
		gTzi:   make([]kyber.Point, expectedB),
		g2zixj: make(map[mkey]kyber.Point, expectedB*expectedB),
		B:      expectedB,
		suite:  suite,
	}
	for i := 0; i < expectedB; i++ {
		setup.G1xi[i], err = readCRSPoint(reader, suite.G1().Point())
		if err != nil {
			return nil, fmt.Errorf("read public CRS G1xi[%d]: %w", i, err)
		}
	}
	for i := 0; i < expectedB; i++ {
		setup.g2zi[i], err = readCRSPoint(reader, suite.G2().Point())
		if err != nil {
			return nil, fmt.Errorf("read public CRS g2zi[%d]: %w", i, err)
		}
		setup.gTzi[i] = suite.Pair(suite.G1().Point().Base(), setup.g2zi[i])
	}
	for i := 0; i < expectedB; i++ {
		for j := 0; j < expectedB; j++ {
			point, pointErr := readCRSPoint(reader, suite.G2().Point())
			if pointErr != nil {
				return nil, fmt.Errorf("read public CRS cross point (%d,%d): %w", i, j, pointErr)
			}
			setup.g2zixj[mkey{i: i, j: j}] = point
		}
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("trailing public CRS bytes: %d", reader.Len())
	}
	return setup, nil
}

func writeCRSPoint(out io.Writer, point kyber.Point) error {
	if point == nil {
		return fmt.Errorf("nil public CRS point")
	}
	encoded, err := point.MarshalBinary()
	if err != nil {
		return err
	}
	return writeCRSBytes(out, encoded)
}

func writeCRSBytes(out io.Writer, encoded []byte) error {
	if len(encoded) > maxCRSPointBytes {
		return fmt.Errorf("public CRS field is too large: %d", len(encoded))
	}
	if err := binary.Write(out, binary.BigEndian, uint32(len(encoded))); err != nil {
		return err
	}
	_, err := out.Write(encoded)
	return err
}

func readCRSPoint(reader *bytes.Reader, point kyber.Point) (kyber.Point, error) {
	encoded, err := readCRSBytes(reader)
	if err != nil {
		return nil, err
	}
	if err := point.UnmarshalBinary(encoded); err != nil {
		return nil, err
	}
	return point, nil
}

func readCRSBytes(reader *bytes.Reader) ([]byte, error) {
	var size uint32
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
		return nil, err
	}
	if size > maxCRSPointBytes || uint64(size) > uint64(reader.Len()) {
		return nil, fmt.Errorf("invalid public CRS field length %d", size)
	}
	encoded := make([]byte, int(size))
	_, err := io.ReadFull(reader, encoded)
	return encoded, err
}

func scalarFromSeed(s kyber.Scalar, seed []byte, domain string, i int) kyber.Scalar {
	h := sha256.New()
	h.Write([]byte("bte-prf-seed-v1"))
	h.Write(seed)
	h.Write([]byte(domain))
	var idx [8]byte
	binary.BigEndian.PutUint64(idx[:], uint64(i))
	h.Write(idx[:])
	return s.SetBytes(h.Sum(nil))
}

func (f *PRF) KeyGen() kyber.Scalar {
	return f.suite.G1().Scalar().Pick(f.suite.RandomStream())
}

func (f *PRF) SumKeys(k []kyber.Scalar) kyber.Scalar {
	sum := f.suite.G1().Scalar().Zero()
	for _, ki := range k {
		sum = sum.Add(sum, ki)
	}
	return sum
}

func (f *PRF) Puncture(k kyber.Scalar, i int) (kyber.Point, error) {
	// Verify that the index is within the domain.
	if i < 0 || i >= f.B {
		return nil, fmt.Errorf("puncturing index out of domain. Domain: [0, %d-1], index: %d", f.B, i)
	}
	return f.suite.G1().Point().Mul(k, f.G1xi[i]), nil
}

func (f *PRF) Eval(k kyber.Scalar, i int) (kyber.Point, error) {
	// Verify that the index is within the domain.
	if i < 0 || i >= f.B {
		return nil, fmt.Errorf("evaluation index out of domain. Domain: [0, %d-1], index: %d", f.B, i)
	}
	return f.suite.GT().Point().Mul(k, f.gTzi[i]), nil
}

func (f *PRF) PEval(kp kyber.Point, pi, i int) (kyber.Point, error) {
	// Verify that the indices are within the domain.
	if i < 0 || i >= f.B {
		return nil, fmt.Errorf("punctured evaluation index out of domain. Domain: [0, %d-1], index: %d", f.B, i)
	}
	// Verify that the punctured index is within the domain.
	if pi < 0 || pi >= f.B {
		return nil, fmt.Errorf("punctured index out of domain for peval. Domain: [0, %d-1], index: %d", f.B, pi)
	}
	if pi == i {
		return nil, fmt.Errorf("punctured index cannot be the same as the evaluation index")
	}
	crselem := f.g2zixj[mkey{
		i: i,
		j: pi,
	}]
	return f.suite.Pair(kp, crselem), nil
}

func (f *PRF) ExpEval(K kyber.Point, i int) (kyber.Point, error) {
	// Verify that the index is within the domain.
	if i < 0 || i >= f.B {
		return nil, fmt.Errorf("exponential evaluation index out of domain. Domain: [0, %d-1], index: %d", f.B, i)
	}
	return f.suite.Pair(K, f.g2zi[i]), nil
}
