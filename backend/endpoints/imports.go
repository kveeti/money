package endpoints

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"money/backend/data"
	"money/backend/state"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	duplicatesPageSize   = 50
	importsPageSize      = 5
	importRequestTimeout = 5 * time.Minute
)

const maxConcurrentUploads = 2

// maxImportBytes caps the upload body; a var so tests can lower it.
var maxImportBytes int64 = 10 << 20
var importUploadSlots = make(chan struct{}, maxConcurrentUploads)

func mapImportErr(err error) error {
	switch {
	case errors.Is(err, data.ErrImportNotFound):
		return NewErr(err.Error(), http.StatusNotFound)
	case errors.Is(err, data.ErrImportBucket), errors.Is(err, data.ErrNotDuplicate):
		return NewErr(err.Error(), http.StatusBadRequest)
	case errors.Is(err, data.ErrImportLimit):
		return NewErr(err.Error(), http.StatusTooManyRequests)
	default:
		return NewUnexpectedErr("import error: %w", err)
	}
}

// HandleCreateImport lands the upload durably and returns immediately; parsing
// and promotion happen in the background worker. It caps the body, checks the
// CSV header, then stores the upload for the worker.
func HandleCreateImport(state *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		deadline := time.Now().Add(importRequestTimeout)
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(deadline); err != nil {
			return NewUnexpectedErr("setting import read deadline: %w", err)
		}
		if err := controller.SetWriteDeadline(deadline); err != nil {
			return NewUnexpectedErr("setting import write deadline: %w", err)
		}

		userID, err := getUserID(r)
		if err != nil {
			return err
		}

		select {
		case importUploadSlots <- struct{}{}:
			defer func() { <-importUploadSlots }()
		default:
			return NewErr("too many uploads; try again later", http.StatusTooManyRequests)
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			if isTooLarge(err) {
				return NewErr("file too large", http.StatusRequestEntityTooLarge)
			}
			return NewErr("invalid multipart form", http.StatusBadRequest)
		}
		bucketID := r.FormValue("bucket_id")
		if bucketID == "" {
			return NewErr("bucket_id is required", http.StatusBadRequest)
		}
		if err := validateUUID("bucket id", bucketID); err != nil {
			return err
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return NewErr("file is required", http.StatusBadRequest)
		}
		defer file.Close()

		br := bufio.NewReader(file)
		firstLine, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			if isTooLarge(err) {
				return NewErr("file too large", http.StatusRequestEntityTooLarge)
			}
			return NewErr("could not read file", http.StatusBadRequest)
		}
		if !data.ValidGenericCSVHeader(firstLine) {
			return NewErr("CSV header must be date,occurred_at,amount,currency,counterparty,note", http.StatusBadRequest)
		}

		upload := io.MultiReader(strings.NewReader(firstLine), br)
		batch, err := state.Data.CreateImport(r.Context(), userID, bucketID, "csv", header.Filename, upload)
		if err != nil {
			return mapImportErr(err)
		}

		JsonStatus(w, http.StatusCreated, JSON{
			"batch_id": batch.ID,
			"status":   batch.Status,
		})
		return nil
	}
}

func isTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

func HandleListImports(state *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}

		cursorDate, cursorID, err := parseCursor(r)
		if err != nil {
			return err
		}
		batches, err := state.Data.ListImports(r.Context(), userID, cursorDate, cursorID, importsPageSize)
		if err != nil {
			return NewUnexpectedErr("error listing imports: %w", err)
		}

		rows := make([]JSON, len(batches))
		for i, b := range batches {
			rows[i] = JSON{
				"id":         b.ID,
				"bucket_id":  b.BucketID,
				"filename":   b.Filename,
				"created_at": b.CreatedAt,
				"status":     b.Status,
				"imported":   b.Imported,
				"duplicates": b.Duplicates,
			}
		}
		var nextCursor *cursor
		if len(batches) == importsPageSize {
			last := batches[len(batches)-1]
			nextCursor = &cursor{Date: formatDate(last.CreatedAt), ID: last.ID}
		}
		Json(w, JSON{"rows": rows, "next_cursor": nextCursor})
		return nil
	}
}

func HandleGetImport(state *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}

		id := r.PathValue("id")
		if err := validateUUID("import id", id); err != nil {
			return err
		}
		batch, err := state.Data.GetImportStatus(r.Context(), userID, id)
		if err != nil {
			return mapImportErr(err)
		}

		parseErrors := batch.ParseErrors
		if parseErrors == nil {
			parseErrors = json.RawMessage("[]")
		}
		Json(w, JSON{
			"id":                batch.ID,
			"bucket_id":         batch.BucketID,
			"filename":          batch.Filename,
			"created_at":        batch.CreatedAt,
			"status":            batch.Status,
			"error":             batch.Error,
			"imported":          batch.Imported,
			"duplicates":        batch.Duplicates,
			"parse_errors":      parseErrors,
			"parse_error_count": batch.ParseErrorCount,
		})
		return nil
	}
}

// HandleListDuplicates serves one keyset page of a batch's duplicate rows for the
// report's infinite list. next_cursor is the last id when a full page came back,
// null when the list is exhausted.
func HandleListDuplicates(state *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}

		id := r.PathValue("id")
		if err := validateUUID("import id", id); err != nil {
			return err
		}
		limit := duplicatesPageSize
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		cursor := r.URL.Query().Get("cursor")
		if cursor != "" {
			if err := validateUUID("cursor", cursor); err != nil {
				return err
			}
		}

		rows, err := state.Data.ListDuplicates(r.Context(), userID, id, cursor, limit)
		if err != nil {
			return NewUnexpectedErr("error listing duplicates: %w", err)
		}

		out := make([]JSON, len(rows))
		for i, row := range rows {
			out[i] = JSON{
				"id":           row.ID,
				"date":         row.OccurredOn.Format(time.DateOnly),
				"occurred_at":  row.OccurredAt,
				"amount":       row.Amount,
				"currency":     row.Currency,
				"counterparty": row.Counterparty,
				"note":         row.Note,
				"duplicate_of": row.DuplicateOf,
			}
			if t := row.Target; t != nil {
				out[i]["duplicate_target"] = JSON{
					"date":           t.OccurredOn.Format(time.DateOnly),
					"occurred_at":    t.OccurredAt,
					"amount":         t.Amount,
					"currency":       t.Currency,
					"counterparty":   t.Counterparty,
					"note":           t.Note,
					"transaction_id": t.TransactionID,
					"batch_id":       t.BatchID,
					"created_at":     t.CreatedAt,
				}
			}
		}

		var nextCursor any
		if len(rows) == limit {
			nextCursor = rows[len(rows)-1].ID
		}

		Json(w, JSON{"rows": out, "next_cursor": nextCursor})
		return nil
	}
}

func HandleForceImportRow(state *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		id := r.PathValue("id")
		if err := validateUUID("import row id", id); err != nil {
			return err
		}
		if err := state.Data.ForceImport(r.Context(), userID, id); err != nil {
			return mapImportErr(err)
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func HandleDeleteImport(state *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}
		id := r.PathValue("id")
		if err := validateUUID("import id", id); err != nil {
			return err
		}
		if err := state.Data.DeleteImport(r.Context(), userID, id); err != nil {
			return mapImportErr(err)
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
