package be

import (
	"bytes"
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
