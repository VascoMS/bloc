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

var evidenceCorpusClasses = []corpusClassSpec{
	{Name: corpusClassTransfer, CalldataBytes: 0, Rows: 28},
	{Name: corpusClass128, CalldataBytes: 128, Rows: 50},
	{Name: corpusClass256, CalldataBytes: 256, Rows: 12},
	{Name: corpusClass1024, CalldataBytes: 1024, Rows: 8},
	{Name: corpusClass4096, CalldataBytes: 4096, Rows: 2},
}

var evidenceCorpusChainID = big.NewInt(1337)

func readEvidenceCorpus(path string) ([]parsedTargetTx, error) {
	targets, err := readTargetCorpus(path)
	if err != nil {
		return nil, err
	}

	expectedRows := 0
	specByClass := make(map[corpusClass]corpusClassSpec, len(evidenceCorpusClasses))
	specByLength := make(map[int]corpusClassSpec, len(evidenceCorpusClasses))
	for _, spec := range evidenceCorpusClasses {
		expectedRows += spec.Rows
		specByClass[spec.Name] = spec
		specByLength[spec.CalldataBytes] = spec
	}
	if len(targets) != expectedRows {
		return nil, fmt.Errorf("evidence corpus rows = %d, want %d", len(targets), expectedRows)
	}

	counts := make(map[corpusClass]int, len(evidenceCorpusClasses))
	hashes := make(map[string]struct{}, len(targets))
	for index := range targets {
		target := &targets[index]
		if target.Tx.Type() != types.DynamicFeeTxType {
			return nil, fmt.Errorf("evidence corpus row %d transaction type = %d, want EIP-1559", index+1, target.Tx.Type())
		}
		if target.Tx.ChainId() == nil || target.Tx.ChainId().Cmp(evidenceCorpusChainID) != 0 {
			return nil, fmt.Errorf("evidence corpus row %d chain id = %v, want %s", index+1, target.Tx.ChainId(), evidenceCorpusChainID)
		}
		if target.Tx.Gas() < 21_000 {
			return nil, fmt.Errorf("evidence corpus row %d intrinsic gas limit = %d, want at least 21000", index+1, target.Tx.Gas())
		}
		spec, ok := specByLength[len(target.Tx.Data())]
		if !ok {
			return nil, fmt.Errorf("evidence corpus row %d calldata bytes = %d, want an exact evidence class size", index+1, len(target.Tx.Data()))
		}
		if target.DeclaredClass == "" {
			return nil, fmt.Errorf("evidence corpus row %d declared class is missing", index+1)
		}
		declaredSpec, ok := specByClass[target.DeclaredClass]
		if !ok {
			return nil, fmt.Errorf("evidence corpus row %d declared class %q is unknown", index+1, target.DeclaredClass)
		}
		if declaredSpec.Name != spec.Name {
			return nil, fmt.Errorf("evidence corpus row %d declared class %q does not match %d calldata bytes", index+1, target.DeclaredClass, len(target.Tx.Data()))
		}
		if _, duplicate := hashes[target.Summary.Hash]; duplicate {
			return nil, fmt.Errorf("evidence corpus row %d duplicate transaction hash %s", index+1, target.Summary.Hash)
		}
		hashes[target.Summary.Hash] = struct{}{}
		target.EvidenceClass = spec.Name
		counts[spec.Name]++
	}

	for _, spec := range evidenceCorpusClasses {
		if counts[spec.Name] != spec.Rows {
			return nil, fmt.Errorf("evidence corpus class distribution for %q = %d, want %d", spec.Name, counts[spec.Name], spec.Rows)
		}
	}
	return targets, nil
}
