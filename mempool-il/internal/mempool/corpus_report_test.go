package mempool

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClientOverheadRowsUseBalancedStableSampling(t *testing.T) {
	corpusPath := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "client-overhead-targets.jsonl")
	targets, err := readClientOverheadCorpus(corpusPath)
	if err != nil {
		t.Fatalf("read evidence corpus: %v", err)
	}
	rows, err := buildClientOverheadRows(targets, 100, func(target parsedTargetTx, index int) (ClientOverheadRow, error) {
		return ClientOverheadRow{
			TargetHash:      target.Summary.Hash,
			RawBytes:        len(target.Raw),
			CiphertextBytes: index + 1,
		}, nil
	})
	if err != nil {
		t.Fatalf("build rows: %v", err)
	}
	if got, want := len(rows), 500; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}

	rowIndex := 0
	seen := make(map[string]bool, len(rows))
	for _, spec := range clientOverheadCorpusClasses {
		for sampleIndex := 0; sampleIndex < 100; sampleIndex++ {
			row := rows[rowIndex]
			if row.Class != string(spec.Name) {
				t.Fatalf("row %d class = %q, want %q", rowIndex, row.Class, spec.Name)
			}
			if row.SampleIndex != sampleIndex {
				t.Fatalf("row %d sample index = %d, want %d", rowIndex, row.SampleIndex, sampleIndex)
			}
			if seen[row.TargetHash] {
				t.Fatalf("target %s measured more than once", row.TargetHash)
			}
			seen[row.TargetHash] = true
			rowIndex++
		}
	}
	if got, want := len(seen), 500; got != want {
		t.Fatalf("distinct targets = %d, want %d", got, want)
	}
}

func TestClientOverheadRowsRequireExactlyOneHundredSamplesPerClass(t *testing.T) {
	for _, samples := range []int{99, 101} {
		_, err := buildClientOverheadRows(nil, samples, nil)
		if err == nil || !strings.Contains(err.Error(), "exactly 100") {
			t.Fatalf("samples %d error = %v, want exactly 100", samples, err)
		}
	}
}

func TestClientOverheadRowsRejectIncompleteClassInsteadOfCycling(t *testing.T) {
	corpusPath := filepath.Join("..", "..", "..", "deploy", "docker-compose", "corpus", "client-overhead-targets.jsonl")
	targets, err := readClientOverheadCorpus(corpusPath)
	if err != nil {
		t.Fatalf("read client corpus: %v", err)
	}
	targets = append([]parsedTargetTx(nil), targets[1:]...)
	_, err = buildClientOverheadRows(targets, 100, func(parsedTargetTx, int) (ClientOverheadRow, error) {
		return ClientOverheadRow{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), `class "transfer" contained 99 targets, want 100`) {
		t.Fatalf("error = %v, want incomplete-class rejection", err)
	}
}

func TestClientOverheadCSVSchema(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "nested", "client_overhead.csv")
	rows := []ClientOverheadRow{{
		Class:                     string(corpusClass128),
		SampleIndex:               0,
		TargetHash:                "0xabc",
		RawBytes:                  128,
		CiphertextBytes:           256,
		PlaceholderBytes:          384,
		CalldataBytes:             324,
		CarrierGasEstimate:        30_000,
		EncryptionUS:              12.5,
		SubmissionSerializationUS: 1.25,
	}}
	if err := writeClientOverheadCSV(outputPath, rows); err != nil {
		t.Fatalf("write CSV: %v", err)
	}

	file, err := os.Open(outputPath)
	if err != nil {
		t.Fatalf("open CSV: %v", err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}
	wantHeader := []string{
		"class", "sample_index", "target_hash", "raw_bytes",
		"ciphertext_bytes", "placeholder_bytes", "calldata_bytes",
		"carrier_gas_estimate", "encryption_us",
		"submission_serialization_us",
	}
	if got := records[0]; !reflect.DeepEqual(got, wantHeader) {
		t.Fatalf("header = %#v, want %#v", got, wantHeader)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
}

func TestSerializeRawSubmission(t *testing.T) {
	encoded, err := serializeRawSubmission([]byte{0x01, 0x02, 0xff})
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	var request map[string]string
	if err := json.Unmarshal(encoded, &request); err != nil {
		t.Fatalf("decode serialized request: %v", err)
	}
	if got, want := request["raw_tx"], "0x0102ff"; got != want {
		t.Fatalf("raw_tx = %q, want %q", got, want)
	}
}

func TestEstimateCarrierGasUsesEIP7623DataFloor(t *testing.T) {
	tests := []struct {
		name     string
		calldata []byte
		want     uint64
	}{
		{name: "empty", calldata: nil, want: 21_000},
		{name: "zero byte", calldata: []byte{0x00}, want: 21_010},
		{name: "nonzero byte", calldata: []byte{0x01}, want: 21_040},
		{name: "mixed", calldata: []byte{0x00, 0x01, 0x02}, want: 21_090},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := estimateCarrierGas(test.calldata); got != test.want {
				t.Fatalf("estimate = %d, want %d", got, test.want)
			}
		})
	}
}

func TestMeasureClientOverheadUsesRealEncryptionBoundary(t *testing.T) {
	dir := t.TempDir()
	_, clusterPath, _, rawTarget := writeReplayFixture(t, dir)
	cluster, err := readReplayCluster(clusterPath)
	if err != nil {
		t.Fatalf("read replay cluster: %v", err)
	}
	encryptor, err := newReplayEncryptor(cluster)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	target, err := parseTargetRawTx("0x" + hexString(rawTarget))
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}

	row, err := measureClientOverhead(target, 0, 7, cluster, encryptor)
	if err != nil {
		t.Fatalf("measure client overhead: %v", err)
	}
	if row.RawBytes != len(rawTarget) {
		t.Fatalf("raw bytes = %d, want %d", row.RawBytes, len(rawTarget))
	}
	if row.CiphertextBytes == 0 {
		t.Fatal("ciphertext bytes = 0")
	}
	if row.PlaceholderBytes <= row.RawBytes {
		t.Fatalf("placeholder bytes = %d, want greater than raw %d", row.PlaceholderBytes, row.RawBytes)
	}
	if got, want := row.CalldataBytes-row.CiphertextBytes, 68; got != want {
		t.Fatalf("calldata overhead = %d, want %d", got, want)
	}
	if row.CarrierGasEstimate < 21_000 {
		t.Fatalf("carrier gas estimate = %d", row.CarrierGasEstimate)
	}
	if row.EncryptionUS < 0 || row.SubmissionSerializationUS < 0 {
		t.Fatalf("negative timing: %+v", row)
	}
}

func hexString(data []byte) string {
	const digits = "0123456789abcdef"
	encoded := make([]byte, len(data)*2)
	for index, value := range data {
		encoded[index*2] = digits[value>>4]
		encoded[index*2+1] = digits[value&0x0f]
	}
	return string(encoded)
}
