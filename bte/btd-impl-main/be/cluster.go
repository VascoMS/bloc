package be

import (
	"btd/elgamal"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"

	"go.dedis.ch/kyber/v4"
	"go.dedis.ch/kyber/v4/share"
	"golang.org/x/crypto/hkdf"
)

const (
	LibraryVersion = "bte-tx-v1"
	defaultSuiteID = "BLS12-381-kilic"
	aeadNonceSize  = 12
	aeadTagSize    = 16
)

type PublicParams struct {
	Version string
	SuiteID string
	BMax    int
	N       int
}

type PublicKey struct {
	Point kyber.Point
}

type SecretShare struct {
	OperatorID int
	Share      *share.PriShare
}

type Ciphertext struct {
	Version       string
	ClusterID     string
	Slot          uint64
	Index         int
	Capsule       CT
	Nonce         []byte
	EncryptedTx   []byte
	PlaintextHash [32]byte
}

// CiphertextScope binds ciphertext processing to the application cluster and
// slot that are currently active. Generic library callers may continue to use
// the unscoped APIs when no single runtime scope exists.
type CiphertextScope struct {
	ClusterID string
	Slot      uint64
}

type DecryptionShare struct {
	OperatorID int
	BatchID    [32]byte
	SubBatchID int
	Share      *share.PubShare
}

type BatchItem struct {
	OriginalPosition int
	Ciphertext       Ciphertext
}

type BatchPlan struct {
	BatchID    [32]byte
	Alpha      int
	SubBatches [][]BatchItem
}

// DecodedBatch keeps parsed ciphertexts paired with the immutable identity of
// the canonical wire bytes they were decoded from.
type DecodedBatch struct {
	ciphertexts []Ciphertext
	batchID     [32]byte
	scope       CiphertextScope
	scopeBound  bool
}

// Len returns the number of decoded ciphertexts.
func (b DecodedBatch) Len() int {
	return len(b.ciphertexts)
}

// Ciphertexts returns independently owned decoded ciphertexts in their
// accepted wire order. Mutating the returned values cannot change this batch.
func (b DecodedBatch) Ciphertexts() []Ciphertext {
	out := make([]Ciphertext, len(b.ciphertexts))
	for i := range b.ciphertexts {
		out[i] = cloneCiphertext(b.ciphertexts[i])
	}
	return out
}

type PlaintextResult struct {
	OriginalPosition int
	RawTx            []byte
	HashOK           bool
	Err              error
}

type ClusterBTE struct {
	Params PublicParams
	PK     PublicKey
	Shares []SecretShare
	btd    *BTD
}

func NewClusterBTE(btd *BTD, pk kyber.Point, shares []*share.PriShare) *ClusterBTE {
	btd.eg.PK = pk
	secretShares := make([]SecretShare, len(shares))
	for i, s := range shares {
		secretShares[i] = SecretShare{OperatorID: int(s.I), Share: s}
	}
	return &ClusterBTE{
		Params: PublicParams{
			Version: LibraryVersion,
			SuiteID: defaultSuiteID,
			BMax:    btd.B,
			N:       btd.B,
		},
		PK:     PublicKey{Point: pk},
		Shares: secretShares,
		btd:    btd,
	}
}

func NewNode(btd *BTD, pk kyber.Point, sk SecretShare, n, t int) *ClusterBTE {
	btd.eg.PK = pk
	btd.T = t
	btd.N = n
	return &ClusterBTE{
		Params: PublicParams{
			Version: LibraryVersion,
			SuiteID: defaultSuiteID,
			BMax:    btd.B,
			N:       btd.B,
		},
		PK:     PublicKey{Point: pk},
		Shares: []SecretShare{sk},
		btd:    btd,
	}
}

func (c *ClusterBTE) EncryptTx(rawTx []byte, index int, clusterID string, slot uint64) (Ciphertext, error) {
	capsuleSecret := c.pickGT()
	aead, err := aeadFromGT(capsuleSecret, aeadAAD(clusterID, slot, index))
	if err != nil {
		return Ciphertext{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Ciphertext{}, err
	}
	encryptedTx := aead.Seal(nil, nonce, rawTx, aeadAAD(clusterID, slot, index))
	aad := aeadAAD(clusterID, slot, index)
	capsule, err := c.btd.EncWithContext(c.PK.Point, index, capsuleSecret, aad)
	if err != nil {
		return Ciphertext{}, err
	}
	return Ciphertext{
		Version:       LibraryVersion,
		ClusterID:     clusterID,
		Slot:          slot,
		Index:         index,
		Capsule:       capsule,
		Nonce:         nonce,
		EncryptedTx:   encryptedTx,
		PlaintextHash: sha256.Sum256(rawTx),
	}, nil
}

func (c *ClusterBTE) pickGT() kyber.Point {
	base := c.btd.suite.Pair(c.btd.suite.G1().Point().Base(), c.btd.suite.G2().Point().Base())
	return base.Mul(c.btd.suite.GT().Scalar().Pick(c.btd.suite.RandomStream()), base)
}

func cloneCiphertext(ct Ciphertext) Ciphertext {
	out := ct
	out.Nonce = append([]byte(nil), ct.Nonce...)
	out.EncryptedTx = append([]byte(nil), ct.EncryptedTx...)
	out.Capsule = CT{
		I:       ct.Capsule.I,
		Gamma:   clonePoint(ct.Capsule.Gamma),
		Kp:      clonePoint(ct.Capsule.Kp),
		C:       elgamal.CT{A: clonePoint(ct.Capsule.C.A), B: clonePoint(ct.Capsule.C.B)},
		Context: append([]byte(nil), ct.Capsule.Context...),
		Pi: Proof{
			Ap:   clonePoint(ct.Capsule.Pi.Ap),
			Bp:   clonePoint(ct.Capsule.Pi.Bp),
			Yp:   clonePoint(ct.Capsule.Pi.Yp),
			KHat: cloneScalar(ct.Capsule.Pi.KHat),
			UHat: cloneScalar(ct.Capsule.Pi.UHat),
		},
	}
	return out
}

func clonePoint(point kyber.Point) kyber.Point {
	if point == nil {
		return nil
	}
	return point.Clone()
}

func cloneScalar(scalar kyber.Scalar) kyber.Scalar {
	if scalar == nil {
		return nil
	}
	return scalar.Clone()
}

func (c *ClusterBTE) PlanBatch(ciphertexts []Ciphertext) (BatchPlan, error) {
	return c.planBatch(ciphertexts, nil)
}

// PlanBatchFor plans an in-memory batch only when every ciphertext belongs to
// the expected application cluster and slot.
func (c *ClusterBTE) PlanBatchFor(ciphertexts []Ciphertext, scope CiphertextScope) (BatchPlan, error) {
	return c.planBatch(ciphertexts, &scope)
}

func (c *ClusterBTE) planBatch(ciphertexts []Ciphertext, scope *CiphertextScope) (BatchPlan, error) {
	alpha, subBatches, err := c.arrangeBatchFor(ciphertexts, scope)
	if err != nil {
		return BatchPlan{}, err
	}
	batchID, err := computeBatchID(ciphertexts)
	if err != nil {
		return BatchPlan{}, err
	}
	return BatchPlan{BatchID: batchID, Alpha: alpha, SubBatches: subBatches}, nil
}

// DecodeBatch parses ordered canonical ciphertext encodings exactly once.
func (c *ClusterBTE) DecodeBatch(encoded [][]byte) (DecodedBatch, error) {
	return c.decodeBatch(encoded, nil)
}

// DecodeBatchFor parses a canonical batch and binds it to the expected
// application cluster and slot.
func (c *ClusterBTE) DecodeBatchFor(encoded [][]byte, scope CiphertextScope) (DecodedBatch, error) {
	return c.decodeBatch(encoded, &scope)
}

func (c *ClusterBTE) decodeBatch(encoded [][]byte, scope *CiphertextScope) (DecodedBatch, error) {
	if len(encoded) > c.Params.BMax {
		return DecodedBatch{}, fmt.Errorf("batch size %d exceeds BMax %d", len(encoded), c.Params.BMax)
	}
	decoded := DecodedBatch{
		ciphertexts: make([]Ciphertext, 0, len(encoded)),
	}
	if scope != nil {
		decoded.scope = *scope
		decoded.scopeBound = true
	}
	for i, raw := range encoded {
		ct, err := c.unmarshalCiphertext(raw, scope)
		if err != nil {
			return DecodedBatch{}, fmt.Errorf("decode ciphertext %d: %w", i, err)
		}
		decoded.ciphertexts = append(decoded.ciphertexts, ct)
	}
	decoded.batchID = computeBatchIDFromEncoded(encoded)
	return decoded, nil
}

// PlanDecodedBatch arranges a decoded batch and reuses the immutable identity
// derived from the canonical encodings accepted by DecodeBatch.
func (c *ClusterBTE) PlanDecodedBatch(decoded DecodedBatch) (BatchPlan, error) {
	var scope *CiphertextScope
	if decoded.scopeBound {
		scope = &decoded.scope
	}
	alpha, subBatches, err := c.arrangeBatchFor(decoded.ciphertexts, scope)
	if err != nil {
		return BatchPlan{}, err
	}
	return BatchPlan{
		BatchID:    decoded.batchID,
		Alpha:      alpha,
		SubBatches: subBatches,
	}, nil
}

// DecodeAndPlanBatch is the one-call API for callers that do not need a timing
// boundary between ciphertext decoding and batch arrangement.
func (c *ClusterBTE) DecodeAndPlanBatch(encoded [][]byte) ([]Ciphertext, BatchPlan, error) {
	return c.decodeAndPlanBatch(encoded, nil)
}

// DecodeAndPlanBatchFor is the scoped one-call decode and planning API.
func (c *ClusterBTE) DecodeAndPlanBatchFor(encoded [][]byte, scope CiphertextScope) ([]Ciphertext, BatchPlan, error) {
	return c.decodeAndPlanBatch(encoded, &scope)
}

func (c *ClusterBTE) decodeAndPlanBatch(encoded [][]byte, scope *CiphertextScope) ([]Ciphertext, BatchPlan, error) {
	decoded, err := c.decodeBatch(encoded, scope)
	if err != nil {
		return nil, BatchPlan{}, err
	}
	plan, err := c.PlanDecodedBatch(decoded)
	if err != nil {
		return nil, BatchPlan{}, err
	}
	return decoded.Ciphertexts(), plan, nil
}

func (c *ClusterBTE) arrangeBatch(ciphertexts []Ciphertext) (int, [][]BatchItem, error) {
	return c.arrangeBatchFor(ciphertexts, nil)
}

func (c *ClusterBTE) arrangeBatchFor(ciphertexts []Ciphertext, scope *CiphertextScope) (int, [][]BatchItem, error) {
	if len(ciphertexts) == 0 {
		return 0, nil, fmt.Errorf("empty batch")
	}
	if len(ciphertexts) > c.Params.BMax {
		return 0, nil, fmt.Errorf("batch size %d exceeds BMax %d", len(ciphertexts), c.Params.BMax)
	}
	counts := make(map[int]int)
	for _, ct := range ciphertexts {
		if err := c.validateCiphertextMetadata(ct, scope); err != nil {
			return 0, nil, err
		}
		counts[ct.Index]++
	}
	alpha := int(math.Ceil(2 * math.Sqrt(float64(len(ciphertexts)))))
	if alpha < 1 {
		alpha = 1
	}
	for _, count := range counts {
		if count > alpha {
			alpha = count
		}
	}
	if alpha > len(ciphertexts) {
		alpha = len(ciphertexts)
	}
	items := make([]BatchItem, len(ciphertexts))
	for i, ct := range ciphertexts {
		items[i] = BatchItem{OriginalPosition: i, Ciphertext: ct}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ci := counts[items[i].Ciphertext.Index]
		cj := counts[items[j].Ciphertext.Index]
		if ci != cj {
			return ci > cj
		}
		return items[i].OriginalPosition < items[j].OriginalPosition
	})
	subBatches := make([][]BatchItem, alpha)
	for i, item := range items {
		subBatches[i%alpha] = append(subBatches[i%alpha], item)
	}
	if err := validateSubBatchIndices(subBatches); err == nil {
		return alpha, subBatches, nil
	}

	subBatches, err := arrangeCollisionFree(items, alpha)
	if err != nil {
		return 0, nil, err
	}
	if err := validateSubBatchIndices(subBatches); err != nil {
		return 0, nil, fmt.Errorf("collision-free arrangement is invalid: %w", err)
	}
	return alpha, subBatches, nil
}

func arrangeCollisionFree(items []BatchItem, alpha int) ([][]BatchItem, error) {
	subBatches := make([][]BatchItem, alpha)
	seen := make([]map[int]bool, alpha)
	for i := range seen {
		seen[i] = make(map[int]bool)
	}
	for _, item := range items {
		index := item.Ciphertext.Index
		selected := -1
		for subBatchID := 0; subBatchID < alpha; subBatchID++ {
			if seen[subBatchID][index] {
				continue
			}
			if selected == -1 || len(subBatches[subBatchID]) < len(subBatches[selected]) {
				selected = subBatchID
			}
		}
		if selected == -1 {
			return nil, fmt.Errorf("no collision-free sub-batch available for index %d", index)
		}
		subBatches[selected] = append(subBatches[selected], item)
		seen[selected][index] = true
	}
	return subBatches, nil
}

func validateSubBatchIndices(subBatches [][]BatchItem) error {
	for id, subBatch := range subBatches {
		seen := make(map[int]bool)
		for _, item := range subBatch {
			idx := item.Ciphertext.Index
			if seen[idx] {
				return fmt.Errorf("duplicate index %d in sub-batch %d", idx, id)
			}
			seen[idx] = true
		}
	}
	return nil
}

func (c *ClusterBTE) MakeShare(sk SecretShare, plan BatchPlan, subBatchID int) (DecryptionShare, error) {
	if subBatchID < 0 || subBatchID >= len(plan.SubBatches) {
		return DecryptionShare{}, fmt.Errorf("sub-batch id out of range: %d", subBatchID)
	}
	cts := capsulesFromItems(plan.SubBatches[subBatchID])
	share, err := c.btd.BatchDecWithShare(cts, sk.Share, true)
	if err != nil {
		return DecryptionShare{}, err
	}
	return DecryptionShare{
		OperatorID: sk.OperatorID,
		BatchID:    plan.BatchID,
		SubBatchID: subBatchID,
		Share:      share,
	}, nil
}

func (c *ClusterBTE) CombineShares(plan BatchPlan, shares []DecryptionShare) ([]PlaintextResult, error) {
	results := make([]PlaintextResult, c.planSize(plan))
	bySubBatch := make(map[int][]*share.PubShare)
	seenOperators := make(map[int]map[int]bool)
	for _, d := range shares {
		if d.BatchID != plan.BatchID {
			return nil, fmt.Errorf("share batch id mismatch from operator %d", d.OperatorID)
		}
		if d.SubBatchID < 0 || d.SubBatchID >= len(plan.SubBatches) {
			return nil, fmt.Errorf("share sub-batch id out of range: %d", d.SubBatchID)
		}
		if seenOperators[d.SubBatchID] == nil {
			seenOperators[d.SubBatchID] = make(map[int]bool)
		}
		if seenOperators[d.SubBatchID][d.OperatorID] {
			return nil, fmt.Errorf("duplicate share from operator %d for sub-batch %d", d.OperatorID, d.SubBatchID)
		}
		seenOperators[d.SubBatchID][d.OperatorID] = true
		bySubBatch[d.SubBatchID] = append(bySubBatch[d.SubBatchID], d.Share)
	}
	for subBatchID, items := range plan.SubBatches {
		subShares := bySubBatch[subBatchID]
		if len(subShares) < c.btd.T {
			return nil, fmt.Errorf("sub-batch %d has %d shares, need %d", subBatchID, len(subShares), c.btd.T)
		}
		sort.Slice(subShares, func(i, j int) bool { return subShares[i].I < subShares[j].I })
		subResults, err := c.combineSubBatch(items, subShares)
		if err != nil {
			return nil, fmt.Errorf("sub-batch %d: %w", subBatchID, err)
		}
		for _, result := range subResults {
			results[result.OriginalPosition] = result
		}
	}
	return results, nil
}

func (c *ClusterBTE) combineSubBatch(items []BatchItem, shares []*share.PubShare) ([]PlaintextResult, error) {
	var firstErr error
	var out []PlaintextResult
	forEachShareCombination(len(shares), c.btd.T, func(indices []int) bool {
		candidateShares := make([]*share.PubShare, len(indices))
		for i, idx := range indices {
			candidateShares[i] = shares[idx]
		}
		msgs, _, err := c.btd.BatchCombineMessages(capsulesFromItems(items), candidateShares, true)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return false
		}
		candidateResults := plaintextResultsFromMessages(items, msgs)
		for _, result := range candidateResults {
			if result.Err != nil || !result.HashOK {
				if firstErr == nil {
					firstErr = result.Err
				}
				return false
			}
		}
		out = candidateResults
		return true
	})
	if out == nil {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("no valid threshold share subset")
	}
	return out, nil
}

func plaintextResultsFromMessages(items []BatchItem, msgs []kyber.Point) []PlaintextResult {
	results := make([]PlaintextResult, len(msgs))
	for i, msg := range msgs {
		item := items[i]
		rawTx, err := decryptTx(msg, item.Ciphertext)
		result := PlaintextResult{OriginalPosition: item.OriginalPosition, Err: err}
		if err == nil {
			result.RawTx = rawTx
			result.HashOK = sha256.Sum256(rawTx) == item.Ciphertext.PlaintextHash
			if !result.HashOK {
				result.Err = fmt.Errorf("plaintext hash mismatch")
			}
		}
		results[i] = result
	}
	return results
}

func forEachShareCombination(n, k int, fn func([]int) bool) {
	if k <= 0 || k > n {
		return
	}
	indices := make([]int, k)
	for i := range indices {
		indices[i] = i
	}
	for {
		if fn(append([]int(nil), indices...)) {
			return
		}
		i := k - 1
		for i >= 0 && indices[i] == i+n-k {
			i--
		}
		if i < 0 {
			return
		}
		indices[i]++
		for j := i + 1; j < k; j++ {
			indices[j] = indices[j-1] + 1
		}
	}
}

func (c *ClusterBTE) planSize(plan BatchPlan) int {
	maxPosition := -1
	for _, subBatch := range plan.SubBatches {
		for _, item := range subBatch {
			if item.OriginalPosition > maxPosition {
				maxPosition = item.OriginalPosition
			}
		}
	}
	return maxPosition + 1
}

func capsulesFromItems(items []BatchItem) []CT {
	cts := make([]CT, len(items))
	for i, item := range items {
		cts[i] = item.Ciphertext.Capsule
	}
	return cts
}

func decryptTx(secret kyber.Point, ct Ciphertext) ([]byte, error) {
	if err := validateAEADPayloadShape(ct.Nonce, ct.EncryptedTx); err != nil {
		return nil, err
	}
	aead, err := aeadFromGT(secret, aeadAAD(ct.ClusterID, ct.Slot, ct.Index))
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, ct.Nonce, ct.EncryptedTx, aeadAAD(ct.ClusterID, ct.Slot, ct.Index))
}

func aeadFromGT(secret kyber.Point, aad []byte) (cipher.AEAD, error) {
	secretBytes, err := secret.MarshalBinary()
	if err != nil {
		return nil, err
	}
	reader := hkdf.New(sha256.New, secretBytes, nil, append([]byte(LibraryVersion), aad...))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func aeadAAD(clusterID string, slot uint64, index int) []byte {
	var buf bytes.Buffer
	buf.WriteString(LibraryVersion)
	buf.WriteByte(0)
	buf.WriteString(clusterID)
	buf.WriteByte(0)
	_ = binary.Write(&buf, binary.BigEndian, slot)
	_ = binary.Write(&buf, binary.BigEndian, int64(index))
	return buf.Bytes()
}

func computeBatchID(ciphertexts []Ciphertext) ([32]byte, error) {
	encoded := make([][]byte, 0, len(ciphertexts))
	for _, ct := range ciphertexts {
		raw, err := ct.MarshalBinary()
		if err != nil {
			return [32]byte{}, err
		}
		encoded = append(encoded, raw)
	}
	return computeBatchIDFromEncoded(encoded), nil
}

func computeBatchIDFromEncoded(encoded [][]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(LibraryVersion))
	for _, raw := range encoded {
		_ = binary.Write(h, binary.BigEndian, uint64(len(raw)))
		h.Write(raw)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func (c *ClusterBTE) validateCiphertextMetadata(ct Ciphertext, scope *CiphertextScope) error {
	if ct.Version != LibraryVersion {
		return fmt.Errorf("unsupported ciphertext version %q", ct.Version)
	}
	if scope != nil {
		if ct.ClusterID != scope.ClusterID {
			return fmt.Errorf("ciphertext cluster %q does not match expected cluster %q", ct.ClusterID, scope.ClusterID)
		}
		if ct.Slot != scope.Slot {
			return fmt.Errorf("ciphertext slot %d does not match expected slot %d", ct.Slot, scope.Slot)
		}
	}
	if ct.Index < 0 || ct.Index >= c.Params.N {
		return fmt.Errorf("ciphertext index out of domain: %d", ct.Index)
	}
	if ct.Capsule.I != ct.Index {
		return fmt.Errorf("outer index %d does not match capsule index %d", ct.Index, ct.Capsule.I)
	}
	if !matchesAEADAAD(ct.Capsule.Context, ct.ClusterID, ct.Slot, ct.Index) {
		return fmt.Errorf("ciphertext context does not match metadata")
	}
	return validateAEADPayloadShape(ct.Nonce, ct.EncryptedTx)
}

func matchesAEADAAD(context []byte, clusterID string, slot uint64, index int) bool {
	prefixLen := len(LibraryVersion) + 1 + len(clusterID) + 1
	if len(context) != prefixLen+16 {
		return false
	}
	if string(context[:len(LibraryVersion)]) != LibraryVersion || context[len(LibraryVersion)] != 0 {
		return false
	}
	clusterStart := len(LibraryVersion) + 1
	clusterEnd := clusterStart + len(clusterID)
	if string(context[clusterStart:clusterEnd]) != clusterID || context[clusterEnd] != 0 {
		return false
	}
	return binary.BigEndian.Uint64(context[prefixLen:prefixLen+8]) == slot &&
		int64(binary.BigEndian.Uint64(context[prefixLen+8:])) == int64(index)
}

func validateAEADPayloadShape(nonce, encryptedTx []byte) error {
	if len(nonce) != aeadNonceSize {
		return fmt.Errorf("invalid AEAD nonce length %d, expected %d", len(nonce), aeadNonceSize)
	}
	if len(encryptedTx) < aeadTagSize {
		return fmt.Errorf("encrypted transaction length %d is smaller than AEAD tag size %d", len(encryptedTx), aeadTagSize)
	}
	return nil
}

func (ct Ciphertext) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(ct.Version)
	buf.WriteByte(0)
	buf.WriteString(ct.ClusterID)
	buf.WriteByte(0)
	_ = binary.Write(&buf, binary.BigEndian, ct.Slot)
	_ = binary.Write(&buf, binary.BigEndian, int64(ct.Index))
	if err := writeBytes(&buf, ct.Nonce); err != nil {
		return nil, err
	}
	if err := writeBytes(&buf, ct.EncryptedTx); err != nil {
		return nil, err
	}
	buf.Write(ct.PlaintextHash[:])
	capsule, err := ct.Capsule.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if err := writeBytes(&buf, capsule); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *ClusterBTE) UnmarshalCiphertext(data []byte) (Ciphertext, error) {
	return c.unmarshalCiphertext(data, nil)
}

func (c *ClusterBTE) unmarshalCiphertext(data []byte, scope *CiphertextScope) (Ciphertext, error) {
	reader := bytes.NewReader(data)
	version, err := readNullTerminated(reader)
	if err != nil {
		return Ciphertext{}, err
	}
	if version != LibraryVersion {
		return Ciphertext{}, fmt.Errorf("unsupported ciphertext version %q", version)
	}
	clusterID, err := readNullTerminated(reader)
	if err != nil {
		return Ciphertext{}, err
	}
	var slot uint64
	if err := binary.Read(reader, binary.BigEndian, &slot); err != nil {
		return Ciphertext{}, err
	}
	var index int64
	if err := binary.Read(reader, binary.BigEndian, &index); err != nil {
		return Ciphertext{}, err
	}
	if scope != nil {
		if clusterID != scope.ClusterID {
			return Ciphertext{}, fmt.Errorf("ciphertext cluster %q does not match expected cluster %q", clusterID, scope.ClusterID)
		}
		if slot != scope.Slot {
			return Ciphertext{}, fmt.Errorf("ciphertext slot %d does not match expected slot %d", slot, scope.Slot)
		}
	}
	if index < 0 || index >= int64(c.Params.N) {
		return Ciphertext{}, fmt.Errorf("ciphertext index out of domain: %d", index)
	}
	nonce, err := readBytes(reader)
	if err != nil {
		return Ciphertext{}, err
	}
	if len(nonce) != aeadNonceSize {
		return Ciphertext{}, fmt.Errorf("invalid AEAD nonce length %d, expected %d", len(nonce), aeadNonceSize)
	}
	encryptedTx, err := readBytes(reader)
	if err != nil {
		return Ciphertext{}, err
	}
	if len(encryptedTx) < aeadTagSize {
		return Ciphertext{}, fmt.Errorf("encrypted transaction length %d is smaller than AEAD tag size %d", len(encryptedTx), aeadTagSize)
	}
	var plaintextHash [32]byte
	if _, err := io.ReadFull(reader, plaintextHash[:]); err != nil {
		return Ciphertext{}, err
	}
	capsuleBytes, err := readBytes(reader)
	if err != nil {
		return Ciphertext{}, err
	}
	capsule, err := c.btd.UnmarshalCT(capsuleBytes)
	if err != nil {
		return Ciphertext{}, err
	}
	if reader.Len() != 0 {
		return Ciphertext{}, fmt.Errorf("trailing ciphertext bytes: %d", reader.Len())
	}
	decoded := Ciphertext{
		Version:       version,
		ClusterID:     clusterID,
		Slot:          slot,
		Index:         int(index),
		Capsule:       capsule,
		Nonce:         nonce,
		EncryptedTx:   encryptedTx,
		PlaintextHash: plaintextHash,
	}
	if err := c.validateCiphertextMetadata(decoded, scope); err != nil {
		return Ciphertext{}, err
	}
	return decoded, nil
}

func (ct CT) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, int64(ct.I))
	if err := writeBytes(&buf, ct.Context); err != nil {
		return nil, err
	}
	for _, point := range []kyber.Point{ct.Gamma, ct.Kp, ct.C.A, ct.C.B, ct.Pi.Ap, ct.Pi.Bp, ct.Pi.Yp} {
		encoded, err := point.MarshalBinary()
		if err != nil {
			return nil, err
		}
		if err := writeBytes(&buf, encoded); err != nil {
			return nil, err
		}
	}
	for _, scalar := range []kyber.Scalar{ct.Pi.KHat, ct.Pi.UHat} {
		encoded, err := scalar.MarshalBinary()
		if err != nil {
			return nil, err
		}
		if err := writeBytes(&buf, encoded); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func (b *BTD) UnmarshalCT(data []byte) (CT, error) {
	reader := bytes.NewReader(data)
	var index int64
	if err := binary.Read(reader, binary.BigEndian, &index); err != nil {
		return CT{}, err
	}
	context, err := readBytes(reader)
	if err != nil {
		return CT{}, err
	}
	points := []kyber.Point{
		b.suite.GT().Point(),
		b.suite.G1().Point(),
		b.suite.G1().Point(),
		b.suite.G1().Point(),
		b.suite.G1().Point(),
		b.suite.G1().Point(),
		b.suite.G1().Point(),
	}
	for _, point := range points {
		encoded, err := readBytes(reader)
		if err != nil {
			return CT{}, err
		}
		if err := point.UnmarshalBinary(encoded); err != nil {
			return CT{}, err
		}
	}
	scalars := []kyber.Scalar{
		b.suite.G1().Scalar(),
		b.suite.G1().Scalar(),
	}
	for _, scalar := range scalars {
		encoded, err := readBytes(reader)
		if err != nil {
			return CT{}, err
		}
		if err := scalar.UnmarshalBinary(encoded); err != nil {
			return CT{}, err
		}
	}
	if reader.Len() != 0 {
		return CT{}, fmt.Errorf("trailing capsule bytes: %d", reader.Len())
	}
	return CT{
		I:       int(index),
		Context: context,
		Gamma:   points[0],
		Kp:      points[1],
		C: elgamal.CT{
			A: points[2],
			B: points[3],
		},
		Pi: Proof{
			Ap:   points[4],
			Bp:   points[5],
			Yp:   points[6],
			KHat: scalars[0],
			UHat: scalars[1],
		},
	}, nil
}

func writeBytes(buf *bytes.Buffer, data []byte) error {
	if err := binary.Write(buf, binary.BigEndian, uint64(len(data))); err != nil {
		return err
	}
	_, err := buf.Write(data)
	return err
}

func readBytes(reader *bytes.Reader) ([]byte, error) {
	var size uint64
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
		return nil, err
	}
	if size > uint64(reader.Len()) {
		return nil, fmt.Errorf("encoded size %d exceeds remaining bytes %d", size, reader.Len())
	}
	out := make([]byte, size)
	_, err := io.ReadFull(reader, out)
	return out, err
}

func readNullTerminated(reader *bytes.Reader) (string, error) {
	var out []byte
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if b == 0 {
			return string(out), nil
		}
		out = append(out, b)
	}
}
