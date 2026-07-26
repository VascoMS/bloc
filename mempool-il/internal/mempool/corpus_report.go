package mempool

import (
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"btd/be"
)

var clientOverheadCSVHeader = []string{
	"class",
	"sample_index",
	"target_hash",
	"raw_bytes",
	"ciphertext_bytes",
	"placeholder_bytes",
	"calldata_bytes",
	"carrier_gas_estimate",
	"encryption_us",
	"submission_serialization_us",
}

type ClientOverheadConfig struct {
	CorpusPath      string
	ClusterPath     string
	OutputPath      string
	Slot            uint64
	SamplesPerClass int
}

type ClientOverheadRow struct {
	Class                     string
	SampleIndex               int
	TargetHash                string
	RawBytes                  int
	CiphertextBytes           int
	PlaceholderBytes          int
	CalldataBytes             int
	CarrierGasEstimate        uint64
	EncryptionUS              float64
	SubmissionSerializationUS float64
}

type clientOverheadMeasurer func(target parsedTargetTx, index int) (ClientOverheadRow, error)

type rawSubmissionRequest struct {
	RawTx string `json:"raw_tx"`
}

func WriteClientOverheadReport(cfg ClientOverheadConfig) error {
	if strings.TrimSpace(cfg.CorpusPath) == "" {
		return fmt.Errorf("client overhead report requires a corpus path")
	}
	if strings.TrimSpace(cfg.ClusterPath) == "" {
		return fmt.Errorf("client overhead report requires a cluster config path")
	}
	if strings.TrimSpace(cfg.OutputPath) == "" {
		return fmt.Errorf("client overhead report requires an output path")
	}
	if cfg.Slot == 0 {
		return fmt.Errorf("client overhead report slot must be greater than zero")
	}
	if cfg.SamplesPerClass != 100 {
		return fmt.Errorf("client overhead report samples per class = %d, want exactly 100", cfg.SamplesPerClass)
	}

	targets, err := readClientOverheadCorpus(cfg.CorpusPath)
	if err != nil {
		return fmt.Errorf("read client overhead corpus: %w", err)
	}
	cluster, err := readReplayCluster(cfg.ClusterPath)
	if err != nil {
		return fmt.Errorf("read replay cluster: %w", err)
	}
	encryptor, err := newReplayEncryptor(cluster)
	if err != nil {
		return fmt.Errorf("create replay encryptor: %w", err)
	}
	rows, err := buildClientOverheadRows(targets, cfg.SamplesPerClass, func(target parsedTargetTx, index int) (ClientOverheadRow, error) {
		return measureClientOverhead(target, index, cfg.Slot, cluster, encryptor)
	})
	if err != nil {
		return err
	}
	return writeClientOverheadCSV(cfg.OutputPath, rows)
}

func buildClientOverheadRows(targets []parsedTargetTx, samplesPerClass int, measure clientOverheadMeasurer) ([]ClientOverheadRow, error) {
	if samplesPerClass != 100 {
		return nil, fmt.Errorf("samples per class = %d, want exactly 100", samplesPerClass)
	}
	if measure == nil {
		return nil, fmt.Errorf("client overhead measurer is required")
	}

	byClass := make(map[corpusClass][]parsedTargetTx, len(clientOverheadCorpusClasses))
	for _, target := range targets {
		byClass[target.EvidenceClass] = append(byClass[target.EvidenceClass], target)
	}

	rows := make([]ClientOverheadRow, 0, len(clientOverheadCorpusClasses)*samplesPerClass)
	globalIndex := 0
	for _, spec := range clientOverheadCorpusClasses {
		classTargets := byClass[spec.Name]
		if len(classTargets) != samplesPerClass {
			return nil, fmt.Errorf("client overhead corpus class %q contained %d targets, want %d", spec.Name, len(classTargets), samplesPerClass)
		}
		for sampleIndex := 0; sampleIndex < samplesPerClass; sampleIndex++ {
			target := classTargets[sampleIndex]
			row, err := measure(target, globalIndex)
			if err != nil {
				return nil, fmt.Errorf("measure class %q sample %d: %w", spec.Name, sampleIndex, err)
			}
			row.Class = string(spec.Name)
			row.SampleIndex = sampleIndex
			row.TargetHash = target.Summary.Hash
			rows = append(rows, row)
			globalIndex++
		}
	}
	return rows, nil
}

func measureClientOverhead(target parsedTargetTx, index int, slot uint64, cluster replayCluster, encryptor *be.ClusterBTE) (ClientOverheadRow, error) {
	serializationStart := time.Now()
	if _, err := serializeRawSubmission(target.Raw); err != nil {
		return ClientOverheadRow{}, err
	}
	serializationDuration := time.Since(serializationStart)

	encryptionStart := time.Now()
	encodedCiphertext, err := encryptReplayTarget(target, index, slot, cluster, encryptor)
	encryptionDuration := time.Since(encryptionStart)
	if err != nil {
		return ClientOverheadRow{}, err
	}

	placeholder, err := buildMockPlaceholderFromCiphertext(target, index, encodedCiphertext)
	if err != nil {
		return ClientOverheadRow{}, err
	}
	placeholderRaw, err := decodeHexStrict(placeholder.PlaceholderTxRaw)
	if err != nil {
		return ClientOverheadRow{}, fmt.Errorf("decode placeholder transaction: %w", err)
	}
	calldata, err := decodeHexStrict(placeholder.Input)
	if err != nil {
		return ClientOverheadRow{}, fmt.Errorf("decode placeholder calldata: %w", err)
	}
	return ClientOverheadRow{
		TargetHash:                target.Summary.Hash,
		RawBytes:                  len(target.Raw),
		CiphertextBytes:           len(encodedCiphertext),
		PlaceholderBytes:          len(placeholderRaw),
		CalldataBytes:             len(calldata),
		CarrierGasEstimate:        estimateCarrierGas(calldata),
		EncryptionUS:              durationMicroseconds(encryptionDuration),
		SubmissionSerializationUS: durationMicroseconds(serializationDuration),
	}, nil
}

func serializeRawSubmission(raw []byte) ([]byte, error) {
	return json.Marshal(rawSubmissionRequest{
		RawTx: "0x" + hex.EncodeToString(raw),
	})
}

func estimateCarrierGas(calldata []byte) uint64 {
	var tokens uint64
	for _, value := range calldata {
		if value == 0 {
			tokens++
		} else {
			tokens += 4
		}
	}
	return 21_000 + 10*tokens
}

func durationMicroseconds(duration time.Duration) float64 {
	return float64(duration.Nanoseconds()) / 1000
}

func writeClientOverheadCSV(path string, rows []ClientOverheadRow) (retErr error) {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("client overhead CSV path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".client-overhead-*.csv")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := file.Close(); retErr == nil && closeErr != nil {
				retErr = closeErr
			}
		}
		_ = os.Remove(tempPath)
	}()

	writer := csv.NewWriter(file)
	if err := writer.Write(clientOverheadCSVHeader); err != nil {
		return err
	}
	for _, row := range rows {
		record := []string{
			row.Class,
			strconv.Itoa(row.SampleIndex),
			row.TargetHash,
			strconv.Itoa(row.RawBytes),
			strconv.Itoa(row.CiphertextBytes),
			strconv.Itoa(row.PlaceholderBytes),
			strconv.Itoa(row.CalldataBytes),
			strconv.FormatUint(row.CarrierGasEstimate, 10),
			strconv.FormatFloat(row.EncryptionUS, 'f', 3, 64),
			strconv.FormatFloat(row.SubmissionSerializationUS, 'f', 3, 64),
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return nil
}
