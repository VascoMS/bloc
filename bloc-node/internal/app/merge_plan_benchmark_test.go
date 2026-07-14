package app

import (
	"bytes"
	"fmt"
	"testing"

	"bloc-node/internal/app/inclusion"
	"btd/be"
	"github.com/anthdm/hbbft"
)

type mergePlanBenchmarkFixture struct {
	cluster     *be.ClusterBTE
	batches     []hbbft.AcceptedBatch
	lists       []InclusionList
	merged      MergedEncryptedSet
	ciphertexts []be.Ciphertext
	encoded     [][]byte
	decoded     be.DecodedBatch
}

var (
	benchmarkListsSink      []InclusionList
	benchmarkAgreedSink     AgreedInclusionSet
	benchmarkMergedSink     MergedEncryptedSet
	benchmarkCiphertextSink []be.Ciphertext
	benchmarkPlanSink       be.BatchPlan
)

func BenchmarkMergePlanAttribution(b *testing.B) {
	for _, nodes := range []int{4, 7} {
		for _, batch := range []int{8, 32, 128} {
			for _, overlap := range []bool{false, true} {
				fixture := newMergePlanBenchmarkFixture(b, nodes, batch, overlap)
				mode := "disjoint"
				if overlap {
					mode = "overlap"
				}
				name := fmt.Sprintf("n%d-b%d-%s", nodes, batch, mode)

				b.Run(name+"/pipeline", func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						lists, err := decodeAcceptedLists(fixture.batches)
						if err != nil {
							b.Fatal(err)
						}
						agreed := inclusion.NewAgreedSet(1, lists)
						merged := inclusion.Merge(1, lists, BlockspaceConfig{}, 128)
						encoded := encodedBenchmarkCiphertexts(merged.Items)
						decoded, err := fixture.cluster.DecodeBatch(encoded)
						if err != nil {
							b.Fatal(err)
						}
						ciphertexts := decoded.Ciphertexts()
						plan, err := fixture.cluster.PlanDecodedBatch(decoded)
						if err != nil {
							b.Fatal(err)
						}
						benchmarkListsSink = lists
						benchmarkAgreedSink = agreed
						benchmarkMergedSink = merged
						benchmarkCiphertextSink = ciphertexts
						benchmarkPlanSink = plan
					}
				})

				b.Run(name+"/proposal-decode", func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						lists, err := decodeAcceptedLists(fixture.batches)
						if err != nil {
							b.Fatal(err)
						}
						benchmarkListsSink = lists
					}
				})

				b.Run(name+"/agreed-set", func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						benchmarkAgreedSink = inclusion.NewAgreedSet(1, fixture.lists)
					}
				})

				b.Run(name+"/merge", func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						benchmarkMergedSink = inclusion.Merge(1, fixture.lists, BlockspaceConfig{}, 128)
					}
				})

				b.Run(name+"/ciphertext-decode", func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						decoded, err := fixture.cluster.DecodeBatch(fixture.encoded)
						if err != nil {
							b.Fatal(err)
						}
						benchmarkCiphertextSink = decoded.Ciphertexts()
					}
				})

				b.Run(name+"/batch-plan", func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						plan, err := fixture.cluster.PlanDecodedBatch(fixture.decoded)
						if err != nil {
							b.Fatal(err)
						}
						benchmarkPlanSink = plan
					}
				})
			}
		}
	}
}

func newMergePlanBenchmarkFixture(tb testing.TB, nodes, batch int, overlap bool) mergePlanBenchmarkFixture {
	tb.Helper()
	seed := bytes.Repeat([]byte{0x42}, 32)
	btd := be.NewBTDFromSeed(newSuite(), 128, seed)
	shares, pk := btd.KeyGen(nodes, 2*((nodes-1)/3)+1)
	cluster := be.NewClusterBTE(btd, pk, shares)

	items := make([]EncryptedPlaceholder, batch)
	for i := range items {
		raw := bytes.Repeat([]byte{byte(i + 1)}, 256)
		ct, err := cluster.EncryptTx(raw, i, "merge-plan-bench", 1)
		if err != nil {
			tb.Fatal(err)
		}
		encoded, err := ct.MarshalBinary()
		if err != nil {
			tb.Fatal(err)
		}
		items[i] = EncryptedPlaceholder{
			Ciphertext:            encoded,
			Gas:                   21_000,
			EffectiveFeePerGasWei: fmt.Sprintf("%d", 1_000_000-i),
			From:                  fmt.Sprintf("0x%040x", i+1),
			Nonce:                 uint64(i),
			Kind:                  "placeholder",
		}
	}

	batches := make([]hbbft.AcceptedBatch, 0, nodes)
	for nodeID := 0; nodeID < nodes; nodeID++ {
		var proposed []EncryptedPlaceholder
		if overlap {
			proposed = append([]EncryptedPlaceholder(nil), items...)
		} else {
			for i := nodeID; i < len(items); i += nodes {
				proposed = append(proposed, items[i])
			}
		}
		encoded, err := inclusion.EncodeList(InclusionList{Slot: 1, OperatorID: uint64(nodeID), Items: proposed})
		if err != nil {
			tb.Fatal(err)
		}
		batches = append(batches, hbbft.AcceptedBatch{ProposerID: uint64(nodeID), Batch: encoded})
	}
	lists, err := decodeAcceptedLists(batches)
	if err != nil {
		tb.Fatal(err)
	}
	merged := inclusion.Merge(1, lists, BlockspaceConfig{}, 128)
	encodedCiphertexts := encodedBenchmarkCiphertexts(merged.Items)
	decoded, err := cluster.DecodeBatch(encodedCiphertexts)
	if err != nil {
		tb.Fatal(err)
	}
	return mergePlanBenchmarkFixture{
		cluster: cluster, batches: batches, lists: lists, merged: merged,
		ciphertexts: decoded.Ciphertexts(), encoded: encodedCiphertexts, decoded: decoded,
	}
}

func decodeBenchmarkCiphertexts(tb testing.TB, cluster *be.ClusterBTE, items []EncryptedPlaceholder) []be.Ciphertext {
	tb.Helper()
	ciphertexts := make([]be.Ciphertext, 0, len(items))
	for _, item := range items {
		ct, err := cluster.UnmarshalCiphertext(item.Ciphertext)
		if err != nil {
			tb.Fatal(err)
		}
		ciphertexts = append(ciphertexts, ct)
	}
	return ciphertexts
}

func encodedBenchmarkCiphertexts(items []EncryptedPlaceholder) [][]byte {
	encoded := make([][]byte, len(items))
	for i := range items {
		encoded[i] = items[i].Ciphertext
	}
	return encoded
}
