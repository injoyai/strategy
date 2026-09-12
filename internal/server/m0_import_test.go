package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type importRowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

type importReportWire struct {
	Format    string           `json:"format"`
	Checksum  string           `json:"checksum"`
	SizeBytes int64            `json:"size_bytes"`
	Columns   []string         `json:"columns"`
	Rows      int              `json:"rows"`
	Errors    []importRowError `json:"errors"`
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func importRequest(s *acceptanceStack, fields map[string]string, files map[string]string, idem string) (int, http.Header, []byte) {
	s.t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if err := mw.SetBoundary("m0-import-acceptance-boundary"); err != nil {
		s.t.Fatalf("set multipart boundary: %v", err)
	}
	fieldNames := make([]string, 0, len(fields))
	for name := range fields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	for _, name := range fieldNames {
		if err := mw.WriteField(name, fields[name]); err != nil {
			s.t.Fatalf("write multipart field %s: %v", name, err)
		}
	}
	fileNames := make([]string, 0, len(files))
	for name := range files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		fw, err := mw.CreateFormFile(name, name)
		if err != nil {
			s.t.Fatalf("create form file %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(files[name])); err != nil {
			s.t.Fatalf("write form file %s: %v", name, err)
		}
	}
	if err := mw.Close(); err != nil {
		s.t.Fatalf("close multipart writer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ts.URL+"/api/v1/imports", body)
	if err != nil {
		s.t.Fatalf("build POST /imports: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("POST /imports: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatalf("POST /imports read: %v", err)
	}
	return resp.StatusCode, resp.Header, raw
}

func getRaw(s *acceptanceStack, path string) (int, http.Header, []byte) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/api/v1"+path, nil)
	if err != nil {
		s.t.Fatalf("build GET %s: %v", path, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatalf("GET %s read: %v", path, err)
	}
	return resp.StatusCode, resp.Header, raw
}

func waitForImportJob(s *acceptanceStack, jobID string) acceptanceJob {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _, raw := getRaw(s, "/jobs/"+jobID)
		if status != http.StatusOK {
			s.t.Fatalf("GET /jobs/%s: status %d body %s", jobID, status, raw)
		}
		var job acceptanceJob
		if err := json.Unmarshal(raw, &job); err != nil {
			s.t.Fatalf("decode job %s: %v", jobID, err)
		}
		switch job.State {
		case "succeeded", "failed", "cancelled":
			return job
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("job %s did not reach a terminal state in time; last state %q", jobID, job.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func decodeImportError(s *acceptanceStack, status int, raw []byte) errorEnvelope {
	s.t.Helper()
	if status < 400 {
		s.t.Fatalf("status %d body %s, want an error response", status, raw)
	}
	var env errorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		s.t.Fatalf("decode error envelope: %v body %s", err, raw)
	}
	if env.Code == "" || env.Message == "" {
		s.t.Fatalf("error envelope missing code/message: %+v", env)
	}
	return env
}

func postImportJob(s *acceptanceStack, fields, files map[string]string, idem string) acceptanceJob {
	s.t.Helper()
	status, _, raw := importRequest(s, fields, files, idem)
	if status != http.StatusAccepted {
		s.t.Fatalf("POST /imports: status %d body %s, want 202", status, raw)
	}
	var job acceptanceJob
	if err := json.Unmarshal(raw, &job); err != nil {
		s.t.Fatalf("decode 202 body: %v body %s", err, raw)
	}
	return job
}

func TestImportCSVBoundaryContract(t *testing.T) {
	s := bootAcceptance(t)
	csv := "instrument_id,time,close\nINST_A,2026-01-05T15:30:00Z,10.50\nINST_B,2026-01-06T15:30:00Z,11.00\n"

	status, header, raw := importRequest(s,
		map[string]string{"format": "csv"},
		map[string]string{"file": csv},
		"import-csv-boundary-01")
	if status != http.StatusAccepted {
		t.Fatalf("POST /imports: status %d body %s, want 202", status, raw)
	}
	var job acceptanceJob
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatalf("decode 202 body: %v body %s", err, raw)
	}
	if job.Kind != "import.validate" {
		t.Fatalf("import job kind %q, want import.validate", job.Kind)
	}
	if loc := header.Get("Location"); loc != "/api/v1/jobs/"+job.ID {
		t.Fatalf("Location %q, want /api/v1/jobs/%s", loc, job.ID)
	}

	job = waitForImportJob(s, job.ID)
	if job.State != "succeeded" {
		t.Fatalf("import job state %q, want succeeded (error: %+v)", job.State, job.Error)
	}
	if len(job.ResultRefs) != 1 || job.ResultRefs[0].Kind != "artifact" || job.ResultRefs[0].ID == "" {
		t.Fatalf("result_refs = %+v, want exactly one artifact ref", job.ResultRefs)
	}
	reportID := job.ResultRefs[0].ID

	status, _, raw = getRaw(s, "/artifacts/"+reportID)
	if status != http.StatusOK {
		t.Fatalf("GET /artifacts/%s: status %d body %s", reportID, status, raw)
	}
	var art artifactWire
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decode artifact: %v body %s", err, raw)
	}
	if art.ID != reportID || art.Name == "" || art.MediaType != "application/json" || art.Checksum == "" || art.SizeBytes <= 0 {
		t.Fatalf("artifact wire incomplete: %+v", art)
	}
	if _, err := time.Parse(time.RFC3339, art.CreatedAt); err != nil {
		t.Fatalf("artifact created_at %q is not RFC3339: %v", art.CreatedAt, err)
	}

	status, header, raw = getRaw(s, "/artifacts/"+reportID+"/content")
	if status != http.StatusOK {
		t.Fatalf("GET /artifacts/%s/content: status %d", reportID, status)
	}
	if ct := header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content Content-Type %q, want artifact media_type", ct)
	}
	if cd := header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("content Content-Disposition %q, want attachment", cd)
	}
	if got := sha256Hex(raw); got != art.Checksum {
		t.Fatalf("content checksum %s, metadata says %s", got, art.Checksum)
	}

	var report importReportWire
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode import report: %v body %s", err, raw)
	}
	want := importReportWire{
		Format:    "csv",
		Checksum:  sha256Hex([]byte(csv)),
		SizeBytes: int64(len(csv)),
		Columns:   []string{"instrument_id", "time", "close"},
		Rows:      2,
		Errors:    []importRowError{},
	}
	if !reflect.DeepEqual(report, want) {
		t.Fatalf("import report = %+v, want %+v", report, want)
	}
}

func TestImportCSVRowErrorsReported(t *testing.T) {
	s := bootAcceptance(t)
	csv := "a,b,c\n1,2,3\n1,2\n1,2,3,4\n"

	job := postImportJob(s,
		map[string]string{"format": "csv"},
		map[string]string{"file": csv},
		"import-csv-rows-000001")
	job = waitForImportJob(s, job.ID)
	if job.State != "succeeded" {
		t.Fatalf("ragged rows must not fail the job; state %q error %+v", job.State, job.Error)
	}
	if len(job.ResultRefs) != 1 {
		t.Fatalf("result_refs = %+v, want one artifact ref", job.ResultRefs)
	}
	_, _, raw := getRaw(s, "/artifacts/"+job.ResultRefs[0].ID+"/content")
	var report importReportWire
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v body %s", err, raw)
	}
	if report.Rows != 3 {
		t.Fatalf("report rows = %d, want 3", report.Rows)
	}
	if len(report.Errors) != 2 {
		t.Fatalf("report errors = %+v, want 2 entries", report.Errors)
	}
	if report.Errors[0].Row != 3 || !strings.Contains(report.Errors[0].Message, "expected 3 columns, got 2") {
		t.Fatalf("first error = %+v, want row 3 with column-count mismatch", report.Errors[0])
	}
	if report.Errors[1].Row != 4 || !strings.Contains(report.Errors[1].Message, "expected 3 columns, got 4") {
		t.Fatalf("second error = %+v, want row 4 with column-count mismatch", report.Errors[1])
	}
}

func TestImportParquetUploadFailsAtProbe(t *testing.T) {
	s := bootAcceptance(t)
	payload := "PAR1" + strings.Repeat("\x00", 96)

	job := postImportJob(s,
		map[string]string{"format": "parquet"},
		map[string]string{"file": payload},
		"import-parquet-probe-01")
	job = waitForImportJob(s, job.ID)
	if job.State != "failed" {
		t.Fatalf("parquet job state %q, want failed (M0 does not claim parquet import)", job.State)
	}
	if job.Error == nil || !strings.Contains(job.Error.Message, "parquet") {
		t.Fatalf("parquet failure error = %+v, want an explicit parquet message", job.Error)
	}
}

func TestImportEmptyCSVFailsJob(t *testing.T) {
	s := bootAcceptance(t)

	job := postImportJob(s,
		map[string]string{"format": "csv"},
		map[string]string{"file": ""},
		"import-csv-empty-0001")
	job = waitForImportJob(s, job.ID)
	if job.State != "failed" {
		t.Fatalf("empty CSV job state %q, want failed", job.State)
	}
	if job.Error == nil || !strings.Contains(job.Error.Message, "empty") {
		t.Fatalf("empty CSV error = %+v, want an explicit empty-file message", job.Error)
	}
}

func TestImportUploadValidation(t *testing.T) {
	s := bootAcceptance(t)
	csv := "a,b\n1,2\n"

	t.Run("format not allowed", func(t *testing.T) {
		status, _, raw := importRequest(s, map[string]string{"format": "xlsx"}, map[string]string{"file": csv}, "import-val-xlsx-0001")
		if status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", status, raw)
		}
		decodeImportError(s, status, raw)
	})
	t.Run("format missing", func(t *testing.T) {
		status, _, raw := importRequest(s, nil, map[string]string{"file": csv}, "import-val-nofmt-001")
		if status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", status, raw)
		}
		decodeImportError(s, status, raw)
	})
	t.Run("file missing", func(t *testing.T) {
		status, _, raw := importRequest(s, map[string]string{"format": "csv"}, nil, "import-val-nofile-01")
		if status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", status, raw)
		}
		decodeImportError(s, status, raw)
	})
	t.Run("unknown field", func(t *testing.T) {
		status, _, raw := importRequest(s, map[string]string{"format": "csv", "comment": "extra"}, map[string]string{"file": csv}, "import-val-extra-001")
		if status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", status, raw)
		}
		decodeImportError(s, status, raw)
	})
	t.Run("idempotency key missing", func(t *testing.T) {
		status, _, raw := importRequest(s, map[string]string{"format": "csv"}, map[string]string{"file": csv}, "")
		if status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", status, raw)
		}
		env := decodeImportError(s, status, raw)
		if !strings.Contains(env.Message, "Idempotency-Key") {
			t.Fatalf("message %q must name Idempotency-Key", env.Message)
		}
	})
	t.Run("idempotency key too short", func(t *testing.T) {
		status, _, raw := importRequest(s, map[string]string{"format": "csv"}, map[string]string{"file": csv}, "short")
		if status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", status, raw)
		}
		decodeImportError(s, status, raw)
	})
}

func TestImportIdempotentReplay(t *testing.T) {
	s := bootAcceptance(t)
	fields := map[string]string{"format": "csv"}
	files := map[string]string{"file": "a,b\n1,2\n"}

	status1, header1, raw1 := importRequest(s, fields, files, "import-replay-0000001")
	if status1 != http.StatusAccepted {
		t.Fatalf("first POST: status %d body %s, want 202", status1, raw1)
	}
	status2, header2, raw2 := importRequest(s, fields, files, "import-replay-0000001")
	if status2 != http.StatusAccepted {
		t.Fatalf("replay POST: status %d body %s, want replayed 202", status2, raw2)
	}
	if header2.Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay response missing Idempotent-Replay header: %v", header2)
	}
	if header1.Get("Location") != header2.Get("Location") {
		t.Fatalf("replay Location %q, want %q", header2.Get("Location"), header1.Get("Location"))
	}
	var first, replayed acceptanceJob
	if err := json.Unmarshal(raw1, &first); err != nil {
		t.Fatalf("decode first body: %v", err)
	}
	if err := json.Unmarshal(raw2, &replayed); err != nil {
		t.Fatalf("decode replay body: %v", err)
	}
	if first.ID != replayed.ID {
		t.Fatalf("replay created a second job %s, first was %s", replayed.ID, first.ID)
	}

	status3, _, raw3 := importRequest(s, fields, map[string]string{"file": "a,b\n3,4\n"}, "import-replay-0000001")
	if status3 != http.StatusConflict {
		t.Fatalf("same key with different payload: status %d body %s, want 409", status3, raw3)
	}
}

func TestArtifactNotFound(t *testing.T) {
	s := bootAcceptance(t)
	status, _, raw := getRaw(s, "/artifacts/art_missing000000")
	if status != http.StatusNotFound {
		t.Fatalf("GET missing artifact: status %d body %s, want 404", status, raw)
	}
	decodeImportError(s, status, raw)
	status, _, raw = getRaw(s, "/artifacts/art_missing000000/content")
	if status != http.StatusNotFound {
		t.Fatalf("GET missing artifact content: status %d, want 404", status)
	}
}
