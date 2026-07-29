package mempool

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
)

type corpusClass string

const (
	corpusClassTransfer corpusClass = "transfer"
	corpusClass128      corpusClass = "calldata_128"
	corpusClass256      corpusClass = "calldata_256"
	corpusClass1024     corpusClass = "calldata_1024"
	corpusClass4096     corpusClass = "calldata_4096"
)

type corpusClassSpec struct {
	Name          corpusClass
	CalldataBytes int
	Rows          int
}

var clientOverheadCorpusClasses = []corpusClassSpec{
	{Name: corpusClassTransfer, CalldataBytes: 0, Rows: 100},
	{Name: corpusClass128, CalldataBytes: 128, Rows: 100},
	{Name: corpusClass256, CalldataBytes: 256, Rows: 100},
	{Name: corpusClass1024, CalldataBytes: 1024, Rows: 100},
	{Name: corpusClass4096, CalldataBytes: 4096, Rows: 100},
}

var protocolWorkloadClasses = []corpusClassSpec{
	{Name: corpusClassTransfer, CalldataBytes: 0, Rows: 143},
	{Name: corpusClass128, CalldataBytes: 128, Rows: 256},
	{Name: corpusClass256, CalldataBytes: 256, Rows: 62},
	{Name: corpusClass1024, CalldataBytes: 1024, Rows: 41},
	{Name: corpusClass4096, CalldataBytes: 4096, Rows: 10},
}

var protocolWorkloadPrefixes = []struct {
	Size   int
	Counts map[corpusClass]int
}{
	{Size: 8, Counts: map[corpusClass]int{
		corpusClassTransfer: 2, corpusClass128: 4, corpusClass256: 1, corpusClass1024: 1, corpusClass4096: 0,
	}},
	{Size: 32, Counts: map[corpusClass]int{
		corpusClassTransfer: 9, corpusClass128: 16, corpusClass256: 4, corpusClass1024: 2, corpusClass4096: 1,
	}},
	{Size: 128, Counts: map[corpusClass]int{
		corpusClassTransfer: 36, corpusClass128: 64, corpusClass256: 15, corpusClass1024: 10, corpusClass4096: 3,
	}},
	{Size: 512, Counts: map[corpusClass]int{
		corpusClassTransfer: 143, corpusClass128: 256, corpusClass256: 62, corpusClass1024: 41, corpusClass4096: 10,
	}},
}

var evidenceCorpusChainID = big.NewInt(1337)

func readClientOverheadCorpus(path string) ([]parsedTargetTx, error) {
	return readStrictCorpus(path, "client overhead corpus", clientOverheadCorpusClasses)
}

func readProtocolWorkloadCorpus(path string) ([]parsedTargetTx, error) {
	targets, err := readStrictCorpus(path, "protocol workload corpus", protocolWorkloadClasses)
	if err != nil {
		return nil, err
	}
	for _, prefix := range protocolWorkloadPrefixes {
		counts := make(map[corpusClass]int, len(protocolWorkloadClasses))
		for _, target := range targets[:prefix.Size] {
			counts[target.EvidenceClass]++
		}
		for class, want := range prefix.Counts {
			if got := counts[class]; got != want {
				return nil, fmt.Errorf(
					"protocol workload corpus prefix %d class distribution for %q = %d, want %d",
					prefix.Size,
					class,
					got,
					want,
				)
			}
		}
	}
	return targets, nil
}

func readStrictCorpus(path, name string, specs []corpusClassSpec) ([]parsedTargetTx, error) {
	targets, err := readTargetCorpus(path)
	if err != nil {
		return nil, err
	}

	expectedRows := 0
	specByClass := make(map[corpusClass]corpusClassSpec, len(specs))
	specByLength := make(map[int]corpusClassSpec, len(specs))
	for _, spec := range specs {
		expectedRows += spec.Rows
		specByClass[spec.Name] = spec
		specByLength[spec.CalldataBytes] = spec
	}
	if len(targets) != expectedRows {
		return nil, fmt.Errorf("%s rows = %d, want %d", name, len(targets), expectedRows)
	}

	counts := make(map[corpusClass]int, len(specs))
	hashes := make(map[string]struct{}, len(targets))
	for index := range targets {
		target := &targets[index]
		if target.Tx.Type() != types.DynamicFeeTxType {
			return nil, fmt.Errorf("%s row %d transaction type = %d, want EIP-1559", name, index+1, target.Tx.Type())
		}
		if target.Tx.ChainId() == nil || target.Tx.ChainId().Cmp(evidenceCorpusChainID) != 0 {
			return nil, fmt.Errorf("%s row %d chain id = %v, want %s", name, index+1, target.Tx.ChainId(), evidenceCorpusChainID)
		}
		minimumGas := estimateCarrierGas(target.Tx.Data())
		if target.Tx.Gas() < minimumGas {
			return nil, fmt.Errorf("%s row %d intrinsic gas limit = %d, want at least %d", name, index+1, target.Tx.Gas(), minimumGas)
		}
		spec, ok := specByLength[len(target.Tx.Data())]
		if !ok {
			return nil, fmt.Errorf("%s row %d calldata bytes = %d, want an exact corpus class size", name, index+1, len(target.Tx.Data()))
		}
		if target.DeclaredClass == "" {
			return nil, fmt.Errorf("%s row %d declared class is missing", name, index+1)
		}
		declaredSpec, ok := specByClass[target.DeclaredClass]
		if !ok {
			return nil, fmt.Errorf("%s row %d declared class %q is unknown", name, index+1, target.DeclaredClass)
		}
		if declaredSpec.Name != spec.Name {
			return nil, fmt.Errorf("%s row %d declared class %q does not match %d calldata bytes", name, index+1, target.DeclaredClass, len(target.Tx.Data()))
		}
		if _, duplicate := hashes[target.Summary.Hash]; duplicate {
			return nil, fmt.Errorf("%s row %d duplicate transaction hash %s", name, index+1, target.Summary.Hash)
		}
		hashes[target.Summary.Hash] = struct{}{}
		target.EvidenceClass = spec.Name
		counts[spec.Name]++
	}

	for _, spec := range specs {
		if counts[spec.Name] != spec.Rows {
			return nil, fmt.Errorf("%s class distribution for %q = %d, want %d", name, spec.Name, counts[spec.Name], spec.Rows)
		}
	}
	return targets, nil
}
