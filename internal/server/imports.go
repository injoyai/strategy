package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/domain"
)

const jobKindImportValidate = "import.validate"

var importFormats = map[string]string{
	"csv":     "text/csv",
	"parquet": "application/x-parquet",
}

func (a *API) RegisterImports() {
	a.Handle(http.MethodPost, "/imports", a.uploadImport, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodGet, "/artifacts/{id}", a.getArtifact, RouteOptions{})
	a.Handle(http.MethodGet, "/artifacts/{id}/content", a.getArtifactContent, RouteOptions{})
}

func (a *API) requireImports(w http.ResponseWriter, r *http.Request) bool {
	if a.artifacts != nil && a.requireDataJobs(w, r) {
		return true
	}
	if a.artifacts == nil {
		a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "artifacts are not mounted on this API"))
	}
	return false
}

func (a *API) uploadImport(w http.ResponseWriter, r *http.Request) {
	if !a.requireImports(w, r) {
		return
	}
	file, filename, format, err := readImportUpload(r)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	mediaType, ok := importFormats[format]
	if !ok {
		a.writeError(w, r, domain.NewError(domain.CodeValidationInvalid,
			"format must be one of csv, parquet"))
		return
	}
	placement, err := a.artifacts.Ingest(bytes.NewReader(file))
	if err != nil {
		a.writeError(w, r, domain.Wrap(err, domain.CodeInternalError, "store uploaded file"))
		return
	}
	_, err = a.artifacts.Record(r.Context(), artifacts.RecordInput{
		Name:      filename,
		MediaType: mediaType,
		Placement: placement,
	})
	if err != nil {
		a.writeError(w, r, domain.Wrap(err, domain.CodeInternalError, "record uploaded artifact"))
		return
	}
	cfg, err := json.Marshal(map[string]any{
		"format":      format,
		"checksum":    placement.Checksum,
		"size":        placement.Size,
		"storage_key": placement.StorageKey,
	})
	if err != nil {
		a.writeError(w, r, domain.Wrap(err, domain.CodeInternalError, "encode import job config"))
		return
	}
	job, err := a.jobs.Create(r.Context(), jobKindImportValidate, cfg, "")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteAccepted(w, r, job.ID, job)
}

func readImportUpload(r *http.Request) (file []byte, filename, format string, err error) {
	mediaType, params, merr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if merr != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return nil, "", "", domain.NewError(domain.CodeValidationInvalid,
			"Content-Type must be multipart/form-data")
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	seenFormat, seenFile := false, false
	for {
		part, perr := mr.NextPart()
		if errors.Is(perr, io.EOF) {
			break
		}
		if perr != nil {
			return nil, "", "", domain.Wrap(perr, domain.CodeValidationInvalid, "parse multipart body")
		}
		switch part.FormName() {
		case "file":
			if seenFile {
				return nil, "", "", domain.NewError(domain.CodeValidationInvalid, "duplicate file field")
			}
			file, perr = io.ReadAll(part)
			if perr != nil {
				return nil, "", "", domain.Wrap(perr, domain.CodeValidationInvalid, "read file field")
			}
			filename = part.FileName()
			seenFile = true
		case "format":
			if seenFormat {
				return nil, "", "", domain.NewError(domain.CodeValidationInvalid, "duplicate format field")
			}
			raw, perr := io.ReadAll(part)
			if perr != nil {
				return nil, "", "", domain.Wrap(perr, domain.CodeValidationInvalid, "read format field")
			}
			format = strings.TrimSpace(string(raw))
			seenFormat = true
		default:
			return nil, "", "", domain.NewError(domain.CodeValidationUnknownField,
				"unknown multipart field %q", part.FormName())
		}
	}
	if !seenFile {
		return nil, "", "", domain.NewError(domain.CodeValidationInvalid, "file field is required")
	}
	if !seenFormat {
		return nil, "", "", domain.NewError(domain.CodeValidationInvalid, "format field is required")
	}
	return file, filename, format, nil
}

type artifactWire struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Checksum  string `json:"checksum"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt string `json:"created_at"`
}

func (a *API) getArtifact(w http.ResponseWriter, r *http.Request) {
	if a.artifacts == nil {
		a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "artifacts are not mounted on this API"))
		return
	}
	art, err := a.artifacts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, artifactWire{
		ID:        art.ID,
		Name:      art.Name,
		MediaType: art.MediaType,
		Checksum:  art.Checksum,
		SizeBytes: art.Size,
		CreatedAt: art.CreatedAt.Format(time.RFC3339),
	})
}

func (a *API) getArtifactContent(w http.ResponseWriter, r *http.Request) {
	if a.artifacts == nil {
		a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "artifacts are not mounted on this API"))
		return
	}
	art, content, err := a.artifacts.Open(r.Context(), r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	defer content.Close()
	w.Header().Set("Content-Type", art.MediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": art.Name}))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, content)
}
