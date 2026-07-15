package be

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"btd/curves"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/kyber/v4"
	"go.dedis.ch/kyber/v4/pairing/bls12381/kilic"
	"go.dedis.ch/kyber/v4/share"
)

func newTestCluster(t testing.TB, bMax, n, threshold int) *ClusterBTE {
	t.Helper()
	suite := curves.NewSuite(kilic.NewBLS12381Suite())
	btd := NewBTD(suite, bMax)
	shares, pk := btd.KeyGen(n, threshold)
	return NewClusterBTE(btd, pk, shares)
}

func TestBatchCombineMessagesReturnsPlaintexts(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	msgs := make([]kyber.Point, 4)
	cts := make([]CT, len(msgs))
	for i := range msgs {
		msgs[i] = cluster.pickGT()
		ct, err := cluster.btd.Enc(cluster.PK.Point, i, msgs[i])
		require.NoError(t, err)
		cts[i] = ct
	}
	decShares := make([]*share.PubShare, cluster.btd.T)
	for i := 0; i < cluster.btd.T; i++ {
		decShare, err := cluster.btd.BatchDec(cts, i, true)
		require.NoError(t, err)
		decShares[i] = decShare
	}
	decrypted, _, err := cluster.btd.BatchCombineMessages(cts, decShares, true)
	require.NoError(t, err)
	require.Len(t, decrypted, len(msgs))
	for i := range msgs {
		require.True(t, decrypted[i].Equal(msgs[i]))
	}
}

func TestVerifyCTRejectsMutations(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	msg := cluster.pickGT()
	ct, err := cluster.btd.EncWithContext(cluster.PK.Point, 1, msg, []byte("metadata"))
	require.NoError(t, err)
	ok, err := cluster.btd.VerifyCT(ct)
	require.NoError(t, err)
	require.True(t, ok)

	tests := map[string]func(CT) CT{
		"gamma": func(mut CT) CT {
			mut.Gamma = cluster.btd.suite.GT().Point().Add(mut.Gamma, cluster.pickGT())
			return mut
		},
		"punctured key": func(mut CT) CT {
			mut.Kp = cluster.btd.suite.G1().Point().Add(mut.Kp, cluster.btd.suite.G1().Point().Base())
			return mut
		},
		"elgamal A": func(mut CT) CT {
			mut.C.A = cluster.btd.suite.G1().Point().Add(mut.C.A, cluster.btd.suite.G1().Point().Base())
			return mut
		},
		"index": func(mut CT) CT {
			mut.I = 2
			return mut
		},
		"proof": func(mut CT) CT {
			mut.Pi.KHat = cluster.btd.suite.G1().Scalar().Add(mut.Pi.KHat, cluster.btd.suite.G1().Scalar().One())
			return mut
		},
		"context": func(mut CT) CT {
			mut.Context = []byte("other-metadata")
			return mut
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			ok, err := cluster.btd.VerifyCT(mutate(ct))
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
}

func TestHybridEncryptionRoundTrip(t *testing.T) {
	cluster := newTestCluster(t, 16, 10, 5)
	rawTxs := [][]byte{
		[]byte("raw ethereum tx 0"),
		[]byte("raw ethereum tx 1"),
		[]byte("raw ethereum tx 2"),
		[]byte("raw ethereum tx 3"),
	}
	ciphertexts := make([]Ciphertext, len(rawTxs))
	for i, rawTx := range rawTxs {
		ct, err := cluster.EncryptTx(rawTx, i, "cluster-a", 123)
		require.NoError(t, err)
		ciphertexts[i] = ct
	}
	plan, err := cluster.PlanBatch(ciphertexts)
	require.NoError(t, err)
	shares := make([]DecryptionShare, 0, cluster.btd.T*plan.Alpha)
	for _, secretShare := range cluster.Shares[:cluster.btd.T] {
		for subBatchID := range plan.SubBatches {
			decShare, err := cluster.MakeShare(secretShare, plan, subBatchID)
			require.NoError(t, err)
			shares = append(shares, decShare)
		}
	}
	results, err := cluster.CombineShares(plan, shares)
	require.NoError(t, err)
	require.Len(t, results, len(rawTxs))
	for i, result := range results {
		require.NoError(t, result.Err)
		require.True(t, result.HashOK)
		require.Equal(t, rawTxs[i], result.RawTx)
	}
}

func TestCombineSharesAcceptsAnyValidThresholdSubset(t *testing.T) {
	cluster := newTestCluster(t, 16, 4, 3)
	rawTxs := make([][]byte, 8)
	ciphertexts := make([]Ciphertext, len(rawTxs))
	for i := range rawTxs {
		rawTxs[i] = []byte(fmt.Sprintf("raw ethereum tx %d", i))
		ct, err := cluster.EncryptTx(rawTxs[i], i, "cluster-a", 123)
		require.NoError(t, err)
		ciphertexts[i] = ct
	}
	plan, err := cluster.PlanBatch(ciphertexts)
	require.NoError(t, err)

	subsets := [][]int{
		{0, 1, 2},
		{0, 1, 3},
		{0, 2, 3},
		{1, 2, 3},
		{3, 1, 2},
	}
	for _, subset := range subsets {
		t.Run(fmt.Sprint(subset), func(t *testing.T) {
			shares := make([]DecryptionShare, 0, len(subset)*plan.Alpha)
			for subBatchID := range plan.SubBatches {
				for _, operatorID := range subset {
					decShare, err := cluster.MakeShare(cluster.Shares[operatorID], plan, subBatchID)
					require.NoError(t, err)
					shares = append(shares, decShare)
				}
			}
			results, err := cluster.CombineShares(plan, shares)
			require.NoError(t, err)
			require.Len(t, results, len(rawTxs))
			for i, result := range results {
				require.NoError(t, result.Err)
				require.True(t, result.HashOK)
				require.Equal(t, rawTxs[i], result.RawTx)
			}
		})
	}
}

func TestCombineSharesSkipsInvalidExtraShareSubset(t *testing.T) {
	cluster := newTestCluster(t, 16, 4, 3)
	rawTx := []byte("raw ethereum tx")
	ct, err := cluster.EncryptTx(rawTx, 0, "cluster-a", 123)
	require.NoError(t, err)
	plan, err := cluster.PlanBatch([]Ciphertext{ct})
	require.NoError(t, err)

	shares := make([]DecryptionShare, 0, 4)
	for _, operatorID := range []int{0, 1, 2, 3} {
		decShare, err := cluster.MakeShare(cluster.Shares[operatorID], plan, 0)
		require.NoError(t, err)
		shares = append(shares, decShare)
	}
	shares[0].Share.V = cluster.btd.suite.G1().Point().Add(shares[0].Share.V, cluster.btd.suite.G1().Point().Base())

	results, err := cluster.CombineShares(plan, shares)

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NoError(t, results[0].Err)
	require.True(t, results[0].HashOK)
	require.Equal(t, rawTx, results[0].RawTx)
}

func TestCiphertextSerializationRoundTrip(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("serialized tx"), 0, "cluster-a", 123)
	require.NoError(t, err)
	encoded, err := ct.MarshalBinary()
	require.NoError(t, err)
	decoded, err := cluster.UnmarshalCiphertext(encoded)
	require.NoError(t, err)
	reencoded, err := decoded.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, encoded, reencoded)
	ok, err := cluster.btd.VerifyCT(decoded.Capsule)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestDecodeAndPlanBatchMatchesLegacyPlan(t *testing.T) {
	cluster := newTestCluster(t, 128, 10, 5)
	for _, batchSize := range []int{8, 32, 128} {
		ciphertexts := make([]Ciphertext, batchSize)
		encoded := make([][]byte, batchSize)
		for i := range ciphertexts {
			ct, err := cluster.EncryptTx([]byte(fmt.Sprintf("canonical-tx-%d", i)), i%17, "cluster-a", 123)
			require.NoError(t, err)
			ciphertexts[i] = ct
			encoded[i], err = ct.MarshalBinary()
			require.NoError(t, err)
		}

		legacy, err := cluster.PlanBatch(ciphertexts)
		require.NoError(t, err)
		decoded, optimized, err := cluster.DecodeAndPlanBatch(encoded)
		require.NoError(t, err)
		require.Len(t, decoded, batchSize)
		requirePlansEquivalent(t, legacy, optimized)
		for i := range decoded {
			reencoded, err := decoded[i].MarshalBinary()
			require.NoError(t, err)
			require.Equal(t, encoded[i], reencoded)
		}
	}
}

func TestDecodeBatchRejectsTrailingNoncanonicalEncoding(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("tx"), 0, "cluster-a", 123)
	require.NoError(t, err)
	encoded, err := ct.MarshalBinary()
	require.NoError(t, err)
	encoded = append(encoded, 0)
	_, err = cluster.DecodeBatch([][]byte{encoded})
	require.ErrorContains(t, err, "trailing ciphertext bytes")
}

func TestPlanDecodedBatchRejectsEmptyBatch(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	decoded, err := cluster.DecodeBatch(nil)
	require.NoError(t, err)
	_, err = cluster.PlanDecodedBatch(decoded)
	require.ErrorContains(t, err, "empty batch")
}

func TestDecodeBatchRejectsUnsupportedVersionBeforeCapsuleDecode(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("tx"), 0, "cluster-a", 123)
	require.NoError(t, err)
	ct.Version = "bte-tx-v2"
	encoded, err := ct.MarshalBinary()
	require.NoError(t, err)
	// Corrupt the capsule after serialization to prove version rejection occurs
	// before any curve decoding.
	encoded[len(encoded)-1] ^= 0xff
	_, err = cluster.DecodeBatch([][]byte{encoded})
	require.ErrorContains(t, err, "unsupported ciphertext version")
}

func TestDecodeBatchReportsFirstMalformedCiphertext(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	valid, err := cluster.EncryptTx([]byte("tx"), 0, "cluster-a", 123)
	require.NoError(t, err)
	badVersion := cloneCiphertext(valid)
	badVersion.Version = "bte-tx-v2"
	badVersionBytes, err := badVersion.MarshalBinary()
	require.NoError(t, err)
	badNonce := cloneCiphertext(valid)
	badNonce.Nonce = []byte{1}
	badNonceBytes, err := badNonce.MarshalBinary()
	require.NoError(t, err)

	_, err = cluster.DecodeBatch([][]byte{badVersionBytes, badNonceBytes})
	require.ErrorContains(t, err, "decode ciphertext 0")
	require.ErrorContains(t, err, "unsupported ciphertext version")
	_, err = cluster.DecodeBatch([][]byte{badNonceBytes, badVersionBytes})
	require.ErrorContains(t, err, "decode ciphertext 0")
	require.ErrorContains(t, err, "nonce length")
}

func TestScopedBatchAPIsRejectForeignContext(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("scoped tx"), 0, "cluster-a", 123)
	require.NoError(t, err)
	encoded, err := ct.MarshalBinary()
	require.NoError(t, err)

	scope := CiphertextScope{ClusterID: "cluster-a", Slot: 123}
	decoded, err := cluster.DecodeBatchFor([][]byte{encoded}, scope)
	require.NoError(t, err)
	scopedPlan, err := cluster.PlanDecodedBatch(decoded)
	require.NoError(t, err)
	directScopedPlan, err := cluster.PlanBatchFor([]Ciphertext{ct}, scope)
	require.NoError(t, err)
	_, oneCallPlan, err := cluster.DecodeAndPlanBatchFor([][]byte{encoded}, scope)
	require.NoError(t, err)
	genericPlan, err := cluster.PlanBatch([]Ciphertext{ct})
	require.NoError(t, err)
	requirePlansEquivalent(t, genericPlan, scopedPlan)
	requirePlansEquivalent(t, genericPlan, directScopedPlan)
	requirePlansEquivalent(t, genericPlan, oneCallPlan)

	_, err = cluster.DecodeBatchFor([][]byte{encoded}, CiphertextScope{ClusterID: "cluster-b", Slot: 123})
	require.ErrorContains(t, err, "expected cluster")
	_, err = cluster.DecodeBatchFor([][]byte{encoded}, CiphertextScope{ClusterID: "cluster-a", Slot: 124})
	require.ErrorContains(t, err, "expected slot")
	_, err = cluster.PlanBatchFor([]Ciphertext{ct}, CiphertextScope{ClusterID: "cluster-b", Slot: 123})
	require.ErrorContains(t, err, "expected cluster")
	_, err = cluster.PlanBatchFor([]Ciphertext{ct}, CiphertextScope{ClusterID: "cluster-a", Slot: 122})
	require.ErrorContains(t, err, "expected slot")

	_, err = cluster.DecodeBatch([][]byte{encoded})
	require.NoError(t, err, "generic batch decoding must remain compatible")
}

func TestDecodeBatchRejectsOversizedBatchBeforeParsing(t *testing.T) {
	cluster := newTestCluster(t, 2, 10, 5)
	_, err := cluster.DecodeBatch([][]byte{[]byte("malformed"), []byte("malformed"), []byte("malformed")})
	require.ErrorContains(t, err, "exceeds BMax")
	require.NotContains(t, err.Error(), "decode ciphertext")
}

func TestCiphertextAEADShapeValidation(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	valid, err := cluster.EncryptTx([]byte("tx"), 0, "cluster-a", 123)
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*Ciphertext)
		want   string
	}{
		{name: "nonce", mutate: func(ct *Ciphertext) { ct.Nonce = bytes.Repeat([]byte{1}, aeadNonceSize-1) }, want: "nonce length"},
		{name: "payload", mutate: func(ct *Ciphertext) { ct.EncryptedTx = bytes.Repeat([]byte{1}, aeadTagSize-1) }, want: "AEAD tag size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneCiphertext(valid)
			test.mutate(&mutated)
			_, err := cluster.PlanBatch([]Ciphertext{mutated})
			require.ErrorContains(t, err, test.want)

			encoded, err := mutated.MarshalBinary()
			require.NoError(t, err)
			_, err = cluster.DecodeBatch([][]byte{encoded})
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestCombineSharesRejectsMutatedNonceWithoutPanic(t *testing.T) {
	cluster := newTestCluster(t, 8, 4, 3)
	ct, err := cluster.EncryptTx([]byte("tx"), 0, "cluster-a", 123)
	require.NoError(t, err)
	plan, err := cluster.PlanBatch([]Ciphertext{ct})
	require.NoError(t, err)

	shares := make([]DecryptionShare, 0, cluster.btd.T)
	for _, secret := range cluster.Shares[:cluster.btd.T] {
		decShare, err := cluster.MakeShare(secret, plan, 0)
		require.NoError(t, err)
		shares = append(shares, decShare)
	}
	plan.SubBatches[0][0].Ciphertext.Nonce = []byte{1}

	var combineErr error
	require.NotPanics(t, func() {
		_, combineErr = cluster.CombineShares(plan, shares)
	})
	require.ErrorContains(t, combineErr, "nonce length")
}

func TestDecodedBatchFreezesCanonicalIdentity(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("identity"), 0, "cluster-a", 123)
	require.NoError(t, err)
	encoded, err := ct.MarshalBinary()
	require.NoError(t, err)
	wantBatchID := computeBatchIDFromEncoded([][]byte{encoded})

	decoded, err := cluster.DecodeBatch([][]byte{encoded})
	require.NoError(t, err)
	encoded[len(encoded)-1] ^= 0xff
	plan, err := cluster.PlanDecodedBatch(decoded)
	require.NoError(t, err)
	require.Equal(t, wantBatchID, plan.BatchID)
}

func TestDecodedBatchCiphertextsAreDeepCopies(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("owned"), 0, "cluster-a", 123)
	require.NoError(t, err)
	encoded, err := ct.MarshalBinary()
	require.NoError(t, err)
	decoded, err := cluster.DecodeBatch([][]byte{encoded})
	require.NoError(t, err)
	require.Equal(t, 1, decoded.Len())

	first := decoded.Ciphertexts()
	first[0].Nonce[0] ^= 0xff
	first[0].EncryptedTx[0] ^= 0xff
	first[0].Capsule.Context[0] ^= 0xff
	first[0].Capsule.Gamma.Null()
	first[0].Capsule.Pi.KHat.Zero()

	second := decoded.Ciphertexts()
	reencoded, err := second[0].MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, encoded, reencoded)
	_, err = cluster.PlanDecodedBatch(decoded)
	require.NoError(t, err)
}

func TestUnmarshalCTRejectsInvalidPointAndScalar(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("tx"), 0, "cluster-a", 123)
	require.NoError(t, err)
	encoded, err := ct.Capsule.MarshalBinary()
	require.NoError(t, err)

	_, err = cluster.btd.UnmarshalCT(overwriteCTField(t, encoded, 1))
	require.Error(t, err, "invalid GT point must be rejected")
	_, err = cluster.btd.UnmarshalCT(overwriteCTField(t, encoded, 8))
	require.Error(t, err, "out-of-range scalar must be rejected")
}

func overwriteCTField(t *testing.T, encoded []byte, target int) []byte {
	t.Helper()
	out := append([]byte(nil), encoded...)
	offset := 8 // capsule index
	for field := 0; field <= target; field++ {
		require.GreaterOrEqual(t, len(out)-offset, 8)
		size := int(binary.BigEndian.Uint64(out[offset : offset+8]))
		start := offset + 8
		end := start + size
		require.LessOrEqual(t, end, len(out))
		if field == target {
			for i := start; i < end; i++ {
				out[i] = 0xff
			}
			return out
		}
		offset = end
	}
	t.Fatalf("field %d not found", target)
	return nil
}

func FuzzCiphertextDecoderCanonical(f *testing.F) {
	cluster := newTestCluster(f, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("canonical-seed"), 0, "cluster-a", 123)
	require.NoError(f, err)
	encoded, err := ct.MarshalBinary()
	require.NoError(f, err)
	f.Add(encoded)
	f.Add([]byte("malformed"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		decoded, err := cluster.UnmarshalCiphertext(raw)
		if err != nil {
			return
		}
		reencoded, err := decoded.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, reencoded) {
			t.Fatalf("decoder accepted a noncanonical encoding")
		}
	})
}

func FuzzCTDecoderCanonical(f *testing.F) {
	cluster := newTestCluster(f, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("canonical-capsule"), 0, "cluster-a", 123)
	require.NoError(f, err)
	encoded, err := ct.Capsule.MarshalBinary()
	require.NoError(f, err)
	f.Add(encoded)
	f.Add(append(append([]byte(nil), encoded...), 0))
	f.Add([]byte("malformed"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		decoded, err := cluster.btd.UnmarshalCT(raw)
		if err != nil {
			return
		}
		reencoded, err := decoded.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, reencoded) {
			t.Fatalf("capsule decoder accepted a noncanonical encoding")
		}
	})
}

func requirePlansEquivalent(t *testing.T, left, right BatchPlan) {
	t.Helper()
	require.Equal(t, left.BatchID, right.BatchID)
	require.Equal(t, left.Alpha, right.Alpha)
	require.Equal(t, len(left.SubBatches), len(right.SubBatches))
	for i := range left.SubBatches {
		require.Equal(t, len(left.SubBatches[i]), len(right.SubBatches[i]))
		for j := range left.SubBatches[i] {
			require.Equal(t, left.SubBatches[i][j].OriginalPosition, right.SubBatches[i][j].OriginalPosition)
			leftEncoded, err := left.SubBatches[i][j].Ciphertext.MarshalBinary()
			require.NoError(t, err)
			rightEncoded, err := right.SubBatches[i][j].Ciphertext.MarshalBinary()
			require.NoError(t, err)
			require.Equal(t, leftEncoded, rightEncoded)
		}
	}
}

func TestPlanBatchSeparatesDuplicateIndices(t *testing.T) {
	cluster := newTestCluster(t, 16, 10, 5)
	ciphertexts := make([]Ciphertext, 6)
	for i := range ciphertexts {
		index := i
		if i < 3 {
			index = 1
		}
		ct, err := cluster.EncryptTx([]byte(fmt.Sprintf("tx-%d", i)), index, "cluster-a", 123)
		require.NoError(t, err)
		ciphertexts[i] = ct
	}
	plan, err := cluster.PlanBatch(ciphertexts)
	require.NoError(t, err)
	for subBatchID, subBatch := range plan.SubBatches {
		seen := make(map[int]bool)
		for _, item := range subBatch {
			idx := item.Ciphertext.Index
			require.False(t, seen[idx], "duplicate index %d in sub-batch %d", idx, subBatchID)
			seen[idx] = true
		}
	}
}

func TestPlanBatchUsesDeterministicCollisionFallback(t *testing.T) {
	cluster := newTestCluster(t, 16, 10, 5)
	indexes := []int{0, 1, 2, 1, 2, 3, 0, 3}
	rawTxs := make([][]byte, len(indexes))
	ciphertexts := make([]Ciphertext, len(indexes))
	for i, index := range indexes {
		rawTxs[i] = []byte(fmt.Sprintf("fallback-tx-%d", i))
		ct, err := cluster.EncryptTx(rawTxs[i], index, "cluster-a", 123)
		require.NoError(t, err)
		ciphertexts[i] = ct
	}

	plan, err := cluster.PlanBatch(ciphertexts)
	require.NoError(t, err)
	require.Equal(t, 6, plan.Alpha)
	require.Equal(t, [][]int{{0, 7}, {1, 6}, {2}, {3}, {4}, {5}}, planPositions(plan))
	require.NoError(t, validateSubBatchIndices(plan.SubBatches))

	second, err := cluster.PlanBatch(ciphertexts)
	require.NoError(t, err)
	require.Equal(t, planPositions(plan), planPositions(second))

	shares := make([]DecryptionShare, 0, cluster.btd.T*plan.Alpha)
	for subBatchID := range plan.SubBatches {
		for _, secret := range cluster.Shares[:cluster.btd.T] {
			decShare, err := cluster.MakeShare(secret, plan, subBatchID)
			require.NoError(t, err)
			shares = append(shares, decShare)
		}
	}
	results, err := cluster.CombineShares(plan, shares)
	require.NoError(t, err)
	for i, result := range results {
		require.NoError(t, result.Err)
		require.Equal(t, rawTxs[i], result.RawTx)
	}
}

func TestPlanBatchPreservesExistingRoundRobinMembership(t *testing.T) {
	cluster := newTestCluster(t, 16, 10, 5)
	ciphertexts := make([]Ciphertext, 8)
	for i := range ciphertexts {
		ct, err := cluster.EncryptTx([]byte(fmt.Sprintf("golden-%d", i)), i%3, "cluster-a", 123)
		require.NoError(t, err)
		ciphertexts[i] = ct
	}
	plan, err := cluster.PlanBatch(ciphertexts)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0, 2}, {1, 5}, {3}, {4}, {6}, {7}}, planPositions(plan))
}

func TestArrangeBatchAlwaysSeparatesIndexesUpToBMax(t *testing.T) {
	cluster := newTestCluster(t, 128, 10, 5)
	for size := 1; size <= cluster.Params.BMax; size++ {
		ciphertexts := make([]Ciphertext, size)
		for i := range ciphertexts {
			index := (i*i + size) % 17
			ciphertexts[i] = structuralCiphertext(index, "cluster-a", 123)
		}
		alpha, subBatches, err := cluster.arrangeBatch(ciphertexts)
		require.NoError(t, err, "size %d", size)
		require.Positive(t, alpha)
		require.NoError(t, validateSubBatchIndices(subBatches), "size %d", size)
	}
}

func structuralCiphertext(index int, clusterID string, slot uint64) Ciphertext {
	return Ciphertext{
		Version:     LibraryVersion,
		ClusterID:   clusterID,
		Slot:        slot,
		Index:       index,
		Capsule:     CT{I: index, Context: aeadAAD(clusterID, slot, index)},
		Nonce:       make([]byte, aeadNonceSize),
		EncryptedTx: make([]byte, aeadTagSize),
	}
}

func planPositions(plan BatchPlan) [][]int {
	out := make([][]int, len(plan.SubBatches))
	for subBatchID, subBatch := range plan.SubBatches {
		out[subBatchID] = make([]int, len(subBatch))
		for i, item := range subBatch {
			out[subBatchID][i] = item.OriginalPosition
		}
	}
	return out
}

func TestBatchDecRejectsDuplicateIndices(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	msgA := cluster.pickGT()
	msgB := cluster.pickGT()
	ctA, err := cluster.btd.Enc(cluster.PK.Point, 1, msgA)
	require.NoError(t, err)
	ctB, err := cluster.btd.Enc(cluster.PK.Point, 1, msgB)
	require.NoError(t, err)
	_, err = cluster.btd.BatchDec([]CT{ctA, ctB}, 0, true)
	require.Error(t, err)
}

func TestPlanBatchRejectsMetadataMismatch(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("tx"), 0, "cluster-a", 123)
	require.NoError(t, err)
	ct.ClusterID = "cluster-b"
	_, err = cluster.PlanBatch([]Ciphertext{ct})
	require.Error(t, err)
}

func TestCombineSharesRequiresThreshold(t *testing.T) {
	cluster := newTestCluster(t, 8, 10, 5)
	ct, err := cluster.EncryptTx([]byte("tx"), 0, "cluster-a", 123)
	require.NoError(t, err)
	plan, err := cluster.PlanBatch([]Ciphertext{ct})
	require.NoError(t, err)
	shares := make([]DecryptionShare, 0, cluster.btd.T-1)
	for _, secretShare := range cluster.Shares[:cluster.btd.T-1] {
		decShare, err := cluster.MakeShare(secretShare, plan, 0)
		require.NoError(t, err)
		shares = append(shares, decShare)
	}
	_, err = cluster.CombineShares(plan, shares)
	require.Error(t, err)
}

func TestBatchPlanDeterministic(t *testing.T) {
	cluster := newTestCluster(t, 16, 10, 5)
	ciphertexts := make([]Ciphertext, 8)
	for i := range ciphertexts {
		ct, err := cluster.EncryptTx([]byte(fmt.Sprintf("tx-%d", i)), i%3, "cluster-a", 123)
		require.NoError(t, err)
		ciphertexts[i] = ct
	}
	first, err := cluster.PlanBatch(ciphertexts)
	require.NoError(t, err)
	second, err := cluster.PlanBatch(ciphertexts)
	require.NoError(t, err)
	require.Equal(t, first.BatchID, second.BatchID)
	require.Equal(t, first.Alpha, second.Alpha)
	require.Equal(t, len(first.SubBatches), len(second.SubBatches))
	for i := range first.SubBatches {
		require.Equal(t, len(first.SubBatches[i]), len(second.SubBatches[i]))
		for j := range first.SubBatches[i] {
			require.Equal(t, first.SubBatches[i][j].OriginalPosition, second.SubBatches[i][j].OriginalPosition)
			left, err := first.SubBatches[i][j].Ciphertext.MarshalBinary()
			require.NoError(t, err)
			right, err := second.SubBatches[i][j].Ciphertext.MarshalBinary()
			require.NoError(t, err)
			require.True(t, bytes.Equal(left, right))
		}
	}
}

func BenchmarkHybridFullPath8(b *testing.B) {
	benchmarkHybridFullPath(b, 8)
}

func BenchmarkHybridFullPath32(b *testing.B) {
	benchmarkHybridFullPath(b, 32)
}

func BenchmarkHybridFullPath128(b *testing.B) {
	benchmarkHybridFullPath(b, 128)
}

func BenchmarkHybridFullPath512(b *testing.B) {
	benchmarkHybridFullPath(b, 512)
}

func BenchmarkBatchPlanningAttribution(b *testing.B) {
	for _, batchSize := range []int{8, 32, 128} {
		cluster := newTestCluster(b, 128, 10, 5)
		ciphertexts := make([]Ciphertext, batchSize)
		for i := range ciphertexts {
			ct, err := cluster.EncryptTx(bytes.Repeat([]byte{byte(i + 1)}, 256), i, "cluster-a", 123)
			if err != nil {
				b.Fatal(err)
			}
			ciphertexts[i] = ct
		}
		encoded := make([][]byte, len(ciphertexts))
		for i := range ciphertexts {
			var err error
			encoded[i], err = ciphertexts[i].MarshalBinary()
			if err != nil {
				b.Fatal(err)
			}
		}
		decoded, err := cluster.DecodeBatch(encoded)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("b%d/arrangement", batchSize), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, _, err := cluster.arrangeBatch(ciphertexts); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("b%d/batch-id", batchSize), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := computeBatchID(ciphertexts); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("b%d/batch-id-encoded", batchSize), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = computeBatchIDFromEncoded(encoded)
			}
		})
		b.Run(fmt.Sprintf("b%d/plan", batchSize), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := cluster.PlanBatch(ciphertexts); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("b%d/plan-decoded", batchSize), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := cluster.PlanDecodedBatch(decoded); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkHybridFullPath(b *testing.B, batchSize int) {
	cluster := newTestCluster(b, batchSize, 10, 5)
	rawTxs := make([][]byte, batchSize)
	for i := range rawTxs {
		rawTxs[i] = []byte(fmt.Sprintf("raw ethereum tx %d", i))
	}
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		ciphertexts := make([]Ciphertext, batchSize)
		for i, rawTx := range rawTxs {
			ct, err := cluster.EncryptTx(rawTx, i, "cluster-a", uint64(iter))
			if err != nil {
				b.Fatal(err)
			}
			ciphertexts[i] = ct
		}
		plan, err := cluster.PlanBatch(ciphertexts)
		if err != nil {
			b.Fatal(err)
		}
		shares := make([]DecryptionShare, 0, cluster.btd.T*plan.Alpha)
		for _, secretShare := range cluster.Shares[:cluster.btd.T] {
			for subBatchID := range plan.SubBatches {
				decShare, err := cluster.MakeShare(secretShare, plan, subBatchID)
				if err != nil {
					b.Fatal(err)
				}
				shares = append(shares, decShare)
			}
		}
		results, err := cluster.CombineShares(plan, shares)
		if err != nil {
			b.Fatal(err)
		}
		if len(results) != batchSize {
			b.Fatalf("got %d results, want %d", len(results), batchSize)
		}
	}
}
