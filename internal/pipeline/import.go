package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
)

const KindImportValidate = "import.validate"

const (
	resultKindArtifact  = "artifact"
	importReportName    = "import-report"
	importReportMedia   = "application/json"
	parquetProbeMessage = "parquet import probe is not available yet: no approved parquet reader dependency"
)

type importValidateConfig struct {
	Format     string `json:"format"`
	Checksum   string `json:"checksum"`
	Size       int64  `json:"size"`
	StorageKey string `json:"storage_key"`
}

type importRowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

type importReport struct {
	Format    string           `json:"format"`
	Checksum  string           `json:"checksum"`
	SizeBytes int64            `json:"size_bytes"`
	Columns   []string         `json:"columns"`
	Rows      int              `json:"rows"`
	Errors    []importRowError `json:"errors"`
}

func (h *Handlers) ImportValidate(ctx context.Context, task *jobs.Task) error {
	if h.Artifacts == nil {
		return domain.NewError(domain.CodeInternalUnavailable, "pipeline: artifacts store is not mounted")
	}
	config, err := h.Jobs.Config(ctx, task.JobID())
	if err != nil {
		return err
	}
	var cfg importValidateConfig
	if err := decodeConfig(config, &cfg); err != nil {
		return err
	}
	raw, err := readStoredArtifact(h.Artifacts, cfg.StorageKey, cfg.Size, cfg.Checksum)
	if err != nil {
		return err
	}
	var report *importReport
	switch cfg.Format {
	case "csv":
		report, err = probeCSV(cfg.Format, cfg.Checksum, cfg.Size, raw)
	case "parquet":
		err = domain.NewError(domain.CodeValidationInvalid, "pipeline: %s", parquetProbeMessage)
	default:
		err = domain.NewError(domain.CodeValidationInvalid,
			"pipeline: unknown import format %q", cfg.Format)
	}
	if err != nil {
		return err
	}
	if err := task.Progress("probed", int64(report.Rows), nil); err != nil {
		return err
	}
	reportRaw, err := json.Marshal(report)
	if err != nil {
		return domain.Wrap(err, domain.CodeInternalError, "pipeline: encode import report")
	}
	placement, err := h.Artifacts.Ingest(bytes.NewReader(reportRaw))
	if err != nil {
		return domain.Wrap(err, domain.CodeInternalError, "pipeline: store import report")
	}
	art, err := h.Artifacts.Record(ctx, artifacts.RecordInput{
		Name:      importReportName,
		MediaType: importReportMedia,
		Placement: placement,
	})
	if err != nil {
		return domain.Wrap(err, domain.CodeInternalError, "pipeline: record import report")
	}
	task.SetResultRefs([]jobs.ResultRef{{Kind: resultKindArtifact, ID: art.ID}})
	return nil
}

func readStoredArtifact(store *artifacts.Store, key string, size int64, checksum string) ([]byte, error) {
	path, err := store.Path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError, "pipeline: open stored upload")
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError, "pipeline: read stored upload")
	}
	sum := sha256.Sum256(raw)
	if int64(len(raw)) != size || hex.EncodeToString(sum[:]) != checksum {
		return nil, domain.NewError(domain.CodeInternalError,
			"pipeline: stored upload does not match recorded checksum/size")
	}
	return raw, nil
}

func probeCSV(format, checksum string, size int64, raw []byte) (*importReport, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, domain.NewError(domain.CodeValidationInvalid, "pipeline: csv file is empty")
	}
	reader := csv.NewReader(bytes.NewReader(raw))
	report := &importReport{
		Format:    format,
		Checksum:  checksum,
		SizeBytes: size,
		Columns:   []string{},
		Errors:    []importRowError{},
	}
	for {
		record, rerr := reader.Read()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			var perr *csv.ParseError
			if !errors.As(rerr, &perr) {
				return nil, domain.Wrap(rerr, domain.CodeInternalError, "pipeline: read csv")
			}
			report.Rows++
			report.Errors = append(report.Errors, importRowError{
				Row:     perr.Line,
				Message: fmt.Sprintf("expected %d columns, got %d", reader.FieldsPerRecord, len(record)),
			})
			continue
		}
		if len(report.Columns) == 0 {
			report.Columns = record
			continue
		}
		report.Rows++
	}
	return report, nil
}
