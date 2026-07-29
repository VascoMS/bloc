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
	LibraryVersion                       = "bte-tx-v2"
	defaultSuiteID                       = "BLS12-381-kilic"
	hybridKeyDomain                      = "bloc-hybrid-key-v2"
	hybridAADDomain                      = "bloc-hybrid-aad-v2"
	batchIDDomain                        = "bloc-batch-v2"
	aeadNonceSize                        = 12
	aeadTagSize                          = 16
	defaultMaxCombineAttemptsPerSubBatch = 256
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
	Capsule     CT
	EncryptedTx []byte
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
	Plaintext []byte
	Err       error
}

type positionedPlaintextResult struct {
	position int
	result   PlaintextResult
}

// CombineOptions bounds invalid-share recovery work independently for every
// planned sub-batch.
type CombineOptions struct {
	MaxAttemptsPerSubBatch  int
	AttemptLimitsBySubBatch []int
}

// CombineStats reports cryptographic subset attempts by sub-batch, including
// attempts made before a failed bounded combination.
type CombineStats struct {
	AttemptsBySubBatch []int
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

// NewClusterBTEFromShares restores a threshold cluster from externally stored
// private shares without requiring the trusted-dealer polynomial.
func NewClusterBTEFromShares(btd *BTD, pk kyber.Point, shares []*share.PriShare, n, t int) (*ClusterBTE, error) {
	if n <= 0 || t <= 0 || t > n {
		return nil, fmt.Errorf("invalid threshold configuration n=%d t=%d", n, t)
	}
	if len(shares) < t {
		return nil, fmt.Errorf("provided %d shares, need threshold %d", len(shares), t)
	}
	seen := make(map[uint32]bool, len(shares))
	for _, secret := range shares {
		if secret == nil || secret.V == nil || int(secret.I) >= n || seen[secret.I] {
			return nil, fmt.Errorf("invalid or duplicate private share")
		}
		seen[secret.I] = true
	}
	btd.T = t
	btd.N = n
	return NewClusterBTE(btd, pk, shares), nil
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

func (c *ClusterBTE) EncryptTx(rawTx []byte, index int) (Ciphertext, error) {
	capsuleSecret := c.pickGT()
	capsule, err := c.btd.Enc(c.PK.Point, index, capsuleSecret)
	if err != nil {
		return Ciphertext{}, err
	}
	capsuleDigest, err := digestCapsule(capsule)
	if err != nil {
		return Ciphertext{}, err
	}
	aead, err := aeadFromGT(capsuleSecret, capsuleDigest)
	if err != nil {
		return Ciphertext{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Ciphertext{}, err
	}
	encryptedTx := append([]byte(nil), nonce...)
	encryptedTx = aead.Seal(encryptedTx, nonce, rawTx, hybridAAD(capsuleDigest))
	return Ciphertext{
		Capsule:     capsule,
		EncryptedTx: encryptedTx,
	}, nil
}

func (c *ClusterBTE) pickGT() kyber.Point {
	base := c.btd.suite.Pair(c.btd.suite.G1().Point().Base(), c.btd.suite.G2().Point().Base())
	return base.Mul(c.btd.suite.GT().Scalar().Pick(c.btd.suite.RandomStream()), base)
}

func cloneCiphertext(ct Ciphertext) Ciphertext {
	out := ct
	out.EncryptedTx = append([]byte(nil), ct.EncryptedTx...)
	out.Capsule = CT{
		I:     ct.Capsule.I,
		Gamma: clonePoint(ct.Capsule.Gamma),
		Kp:    clonePoint(ct.Capsule.Kp),
		C:     elgamal.CT{A: clonePoint(ct.Capsule.C.A), B: clonePoint(ct.Capsule.C.B)},
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
	alpha, subBatches, err := c.arrangeBatch(ciphertexts)
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
	if len(encoded) > c.Params.BMax {
		return DecodedBatch{}, fmt.Errorf("batch size %d exceeds BMax %d", len(encoded), c.Params.BMax)
	}
	decoded := DecodedBatch{
		ciphertexts: make([]Ciphertext, 0, len(encoded)),
	}
	for i, raw := range encoded {
		ct, err := c.UnmarshalCiphertext(raw)
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
	alpha, subBatches, err := c.arrangeBatch(decoded.ciphertexts)
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
	decoded, err := c.DecodeBatch(encoded)
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
	if len(ciphertexts) == 0 {
		return 0, nil, fmt.Errorf("empty batch")
	}
	if len(ciphertexts) > c.Params.BMax {
		return 0, nil, fmt.Errorf("batch size %d exceeds BMax %d", len(ciphertexts), c.Params.BMax)
	}
	counts := make(map[int]int)
	for _, ct := range ciphertexts {
		if err := c.validateCiphertext(ct); err != nil {
			return 0, nil, err
		}
		counts[ct.Capsule.I]++
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
		ci := counts[items[i].Ciphertext.Capsule.I]
		cj := counts[items[j].Ciphertext.Capsule.I]
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
		index := item.Ciphertext.Capsule.I
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
			idx := item.Ciphertext.Capsule.I
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
	results, _, err := c.CombineSharesBounded(plan, shares, CombineOptions{MaxAttemptsPerSubBatch: defaultMaxCombineAttemptsPerSubBatch})
	return results, err
}

// CombineSharesBounded validates candidate ownership and tries deterministic
// threshold subsets up to the supplied per-sub-batch limit.
func (c *ClusterBTE) CombineSharesBounded(plan BatchPlan, shares []DecryptionShare, options CombineOptions) ([]PlaintextResult, CombineStats, error) {
	stats := CombineStats{AttemptsBySubBatch: make([]int, len(plan.SubBatches))}
	if options.MaxAttemptsPerSubBatch < 1 {
		return nil, stats, fmt.Errorf("max attempts per sub-batch must be positive")
	}
	if options.AttemptLimitsBySubBatch != nil && len(options.AttemptLimitsBySubBatch) != len(plan.SubBatches) {
		return nil, stats, fmt.Errorf("attempt limit count %d does not match %d sub-batches", len(options.AttemptLimitsBySubBatch), len(plan.SubBatches))
	}
	results := make([]PlaintextResult, c.planSize(plan))
	bySubBatch := make(map[int][]*share.PubShare)
	seenOperators := make(map[int]map[int]bool)
	for _, d := range shares {
		if d.BatchID != plan.BatchID {
			return nil, stats, fmt.Errorf("share batch id mismatch from operator %d", d.OperatorID)
		}
		if d.SubBatchID < 0 || d.SubBatchID >= len(plan.SubBatches) {
			return nil, stats, fmt.Errorf("share sub-batch id out of range: %d", d.SubBatchID)
		}
		if d.OperatorID < 0 || d.OperatorID >= c.btd.N {
			return nil, stats, fmt.Errorf("share operator id out of range: %d", d.OperatorID)
		}
		if d.Share == nil || d.Share.V == nil {
			return nil, stats, fmt.Errorf("nil share from operator %d", d.OperatorID)
		}
		if int(d.Share.I) != d.OperatorID {
			return nil, stats, fmt.Errorf("share index %d does not match operator %d", d.Share.I, d.OperatorID)
		}
		if seenOperators[d.SubBatchID] == nil {
			seenOperators[d.SubBatchID] = make(map[int]bool)
		}
		if seenOperators[d.SubBatchID][d.OperatorID] {
			return nil, stats, fmt.Errorf("duplicate share from operator %d for sub-batch %d", d.OperatorID, d.SubBatchID)
		}
		seenOperators[d.SubBatchID][d.OperatorID] = true
		bySubBatch[d.SubBatchID] = append(bySubBatch[d.SubBatchID], d.Share)
	}
	for subBatchID, items := range plan.SubBatches {
		subShares := bySubBatch[subBatchID]
		if len(subShares) < c.btd.T {
			return nil, stats, fmt.Errorf("sub-batch %d has %d shares, need %d", subBatchID, len(subShares), c.btd.T)
		}
		sort.Slice(subShares, func(i, j int) bool { return subShares[i].I < subShares[j].I })
		attemptLimit := options.MaxAttemptsPerSubBatch
		if options.AttemptLimitsBySubBatch != nil {
			attemptLimit = options.AttemptLimitsBySubBatch[subBatchID]
		}
		if attemptLimit < 1 {
			return nil, stats, fmt.Errorf("sub-batch %d: combine attempt budget exhausted", subBatchID)
		}
		subResults, attempts, err := c.combineSubBatch(items, subShares, attemptLimit)
		stats.AttemptsBySubBatch[subBatchID] = attempts
		if err != nil {
			return nil, stats, fmt.Errorf("sub-batch %d: %w", subBatchID, err)
		}
		for _, positioned := range subResults {
			results[positioned.position] = positioned.result
		}
	}
	return results, stats, nil
}

func (c *ClusterBTE) combineSubBatch(items []BatchItem, shares []*share.PubShare, maxAttempts int) ([]positionedPlaintextResult, int, error) {
	var firstErr error
	var out []positionedPlaintextResult
	attempts := 0
	forEachShareCombination(len(shares), c.btd.T, func(indices []int) bool {
		if attempts >= maxAttempts {
			return true
		}
		attempts++
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
		for _, positioned := range candidateResults {
			if positioned.result.Err != nil {
				if firstErr == nil {
					firstErr = positioned.result.Err
				}
				return false
			}
		}
		out = candidateResults
		return true
	})
	if out == nil {
		if attempts >= maxAttempts {
			return nil, attempts, fmt.Errorf("combine attempt limit %d exhausted", maxAttempts)
		}
		if firstErr != nil {
			return nil, attempts, firstErr
		}
		return nil, attempts, fmt.Errorf("no valid threshold share subset")
	}
	return out, attempts, nil
}

func plaintextResultsFromMessages(items []BatchItem, msgs []kyber.Point) []positionedPlaintextResult {
	results := make([]positionedPlaintextResult, len(msgs))
	for i, msg := range msgs {
		item := items[i]
		rawTx, err := decryptTx(msg, item.Ciphertext)
		result := PlaintextResult{Err: err}
		if err == nil {
			result.Plaintext = rawTx
		}
		results[i] = positionedPlaintextResult{position: item.OriginalPosition, result: result}
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
	if err := validateAEADPayloadShape(ct.EncryptedTx); err != nil {
		return nil, err
	}
	capsuleDigest, err := digestCapsule(ct.Capsule)
	if err != nil {
		return nil, err
	}
	aead, err := aeadFromGT(secret, capsuleDigest)
	if err != nil {
		return nil, err
	}
	nonce := ct.EncryptedTx[:aeadNonceSize]
	sealed := ct.EncryptedTx[aeadNonceSize:]
	return aead.Open(nil, nonce, sealed, hybridAAD(capsuleDigest))
}

func digestCapsule(capsule CT) ([32]byte, error) {
	encoded, err := capsule.MarshalBinary()
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func aeadFromGT(secret kyber.Point, capsuleDigest [32]byte) (cipher.AEAD, error) {
	secretBytes, err := secret.MarshalBinary()
	if err != nil {
		return nil, err
	}
	info := make([]byte, 0, len(hybridKeyDomain)+len(capsuleDigest))
	info = append(info, hybridKeyDomain...)
	info = append(info, capsuleDigest[:]...)
	reader := hkdf.New(sha256.New, secretBytes, nil, info)
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

func hybridAAD(capsuleDigest [32]byte) []byte {
	aad := make([]byte, 0, len(hybridAADDomain)+len(capsuleDigest))
	aad = append(aad, hybridAADDomain...)
	return append(aad, capsuleDigest[:]...)
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
	h.Write([]byte(batchIDDomain))
	for _, raw := range encoded {
		_ = binary.Write(h, binary.BigEndian, uint64(len(raw)))
		h.Write(raw)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func (c *ClusterBTE) validateCiphertext(ct Ciphertext) error {
	if ct.Capsule.I < 0 || ct.Capsule.I >= c.Params.N {
		return fmt.Errorf("ciphertext index out of domain: %d", ct.Capsule.I)
	}
	return validateAEADPayloadShape(ct.EncryptedTx)
}

func validateAEADPayloadShape(encryptedTx []byte) error {
	minimum := aeadNonceSize + aeadTagSize
	if len(encryptedTx) < minimum {
		return fmt.Errorf("encrypted transaction length %d is smaller than nonce and AEAD tag size %d", len(encryptedTx), minimum)
	}
	return nil
}

func (ct Ciphertext) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(LibraryVersion)
	capsule, err := ct.Capsule.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if err := writeBytes(&buf, capsule); err != nil {
		return nil, err
	}
	if err := writeBytes(&buf, ct.EncryptedTx); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *ClusterBTE) UnmarshalCiphertext(data []byte) (Ciphertext, error) {
	reader := bytes.NewReader(data)
	magic := make([]byte, len(LibraryVersion))
	if _, err := io.ReadFull(reader, magic); err != nil {
		return Ciphertext{}, fmt.Errorf("read ciphertext version: %w", err)
	}
	if string(magic) != LibraryVersion {
		return Ciphertext{}, fmt.Errorf("unsupported ciphertext version %q", string(magic))
	}
	capsuleBytes, err := readBytes(reader)
	if err != nil {
		return Ciphertext{}, err
	}
	capsule, err := c.btd.UnmarshalCT(capsuleBytes)
	if err != nil {
		return Ciphertext{}, err
	}
	encryptedTx, err := readBytes(reader)
	if err != nil {
		return Ciphertext{}, err
	}
	if reader.Len() != 0 {
		return Ciphertext{}, fmt.Errorf("trailing ciphertext bytes: %d", reader.Len())
	}
	decoded := Ciphertext{
		Capsule:     capsule,
		EncryptedTx: encryptedTx,
	}
	if err := c.validateCiphertext(decoded); err != nil {
		return Ciphertext{}, err
	}
	return decoded, nil
}

func (ct CT) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, int64(ct.I))
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
		I:     int(index),
		Gamma: points[0],
		Kp:    points[1],
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
