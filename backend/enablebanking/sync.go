package enablebanking

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"money/backend/data"
)

const (
	syncWorkers     = 2
	syncMaxAttempts = 5
	syncPoll        = time.Second
)

type Syncer struct {
	data   *data.Data
	client *Client
	kick   chan struct{}
}

func NewSyncer(d *data.Data, client *Client) *Syncer {
	return &Syncer{data: d, client: client, kick: make(chan struct{}, 1)}
}

func (s *Syncer) Start(ctx context.Context) {
	for range syncWorkers {
		go s.worker(ctx)
	}
	go s.poll(ctx)
	s.Kick()
}

func (s *Syncer) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Syncer) poll(ctx context.Context) {
	ticker := time.NewTicker(syncPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Kick()
		}
	}
}

func (s *Syncer) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		}
		for {
			job, ok, err := s.data.ClaimEnableBankingSync(ctx)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				slog.Error("claim Enable Banking sync", "err", err)
				break
			}
			if !ok {
				break
			}
			s.Kick() // wake another worker if more jobs are queued
			s.process(ctx, job)
		}
	}
}

func (s *Syncer) process(ctx context.Context, job data.EnableBankingSyncJob) {
	currencies, err := s.data.CurrencyExponents(ctx)
	if err != nil {
		s.retry(ctx, job, err)
		return
	}
	for !job.FetchedAll {
		page, err := s.client.LongestTransactions(
			ctx,
			job.AccountUID,
			job.DateFrom.Format(time.DateOnly),
			job.ContinuationKey,
		)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				if apiErr.Code == "EXPIRED_SESSION" || apiErr.Code == "REVOKED_SESSION" {
					s.fail(ctx, job, errors.New("bank connection needs re-authentication"))
					return
				}
				if apiErr.Status >= 400 && apiErr.Status < 500 && apiErr.Status != 408 && apiErr.Status != 429 {
					s.fail(ctx, job, apiErr)
					return
				}
			}
			s.retry(ctx, job, err)
			return
		}
		rows, rowErrors := normalizeTransactions(page.Transactions, currencies, job.NextSequence)
		fetchedAll := page.ContinuationKey == ""
		if err := s.data.StageEnableBankingPage(
			ctx,
			job,
			rows,
			rowErrors,
			page.ContinuationKey,
			fetchedAll,
		); err != nil {
			if errors.Is(err, data.ErrBankSyncRepeatedContinuation) {
				s.fail(ctx, job, err)
			} else {
				s.retry(ctx, job, err)
			}
			return
		}
		job.NextSequence += int64(len(rows))
		job.ContinuationKey = page.ContinuationKey
		job.FetchedAll = fetchedAll
		job.Attempts = 0
	}

	var added, duplicates int
	for attempt := 0; attempt < syncMaxAttempts; attempt++ {
		added, duplicates, err = s.data.FinalizeEnableBankingSync(ctx, job)
		if err == nil {
			slog.Info("Enable Banking sync done", "batch", job.BatchID, "added", added, "duplicates", duplicates)
			return
		}
		if ctx.Err() != nil {
			return
		}
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	s.fail(ctx, job, err)
}

func (s *Syncer) retry(ctx context.Context, job data.EnableBankingSyncJob, cause error) {
	if job.Attempts+1 >= syncMaxAttempts {
		s.fail(ctx, job, cause)
		return
	}
	delay := time.Second * time.Duration(1<<job.Attempts)
	if err := s.data.RetryEnableBankingSync(ctx, job, time.Now().Add(delay), cause); err != nil {
		slog.Error("queue Enable Banking sync retry", "batch", job.BatchID, "err", err)
	}
}

func (s *Syncer) fail(ctx context.Context, job data.EnableBankingSyncJob, cause error) {
	if err := s.data.FailEnableBankingSync(ctx, job, cause); err != nil {
		slog.Error("fail Enable Banking sync", "batch", job.BatchID, "err", err)
		return
	}
	slog.Error("Enable Banking sync failed", "batch", job.BatchID, "err", cause)
}

func normalizeTransactions(transactions []Transaction, currencies map[string]int, firstSequence int64) ([]data.ParsedRow, []data.RowError) {
	rows := make([]data.ParsedRow, 0, len(transactions))
	var rowErrors []data.RowError
	for i, transaction := range transactions {
		if transaction.Status != "" && transaction.Status != "BOOK" {
			continue
		}
		line := int(firstSequence) + i + 1
		dateText := transaction.BookingDate
		if dateText == "" {
			dateText = transaction.TransactionDate
		}
		if dateText == "" {
			dateText = transaction.ValueDate
		}
		occurredOn, err := time.Parse(time.DateOnly, dateText)
		if err != nil {
			rowErrors = append(rowErrors, data.RowError{Line: line, Error: "invalid bank transaction date"})
			continue
		}
		rawAmount := strings.TrimSpace(transaction.TransactionAmount.Amount)
		rawAmount = strings.TrimPrefix(strings.TrimPrefix(rawAmount, "+"), "-")
		switch transaction.CreditDebitIndicator {
		case "DBIT":
			rawAmount = "-" + rawAmount
		case "CRDT":
		default:
			rowErrors = append(rowErrors, data.RowError{Line: line, Error: "invalid bank transaction direction"})
			continue
		}
		amount, currency, err := data.ParseCanonicalAmount(rawAmount, transaction.TransactionAmount.Currency, currencies)
		if err != nil {
			rowErrors = append(rowErrors, data.RowError{Line: line, Error: err.Error()})
			continue
		}
		counterparty := ""
		if transaction.CreditDebitIndicator == "DBIT" && transaction.Creditor != nil {
			counterparty = strings.TrimSpace(transaction.Creditor.Name)
		} else if transaction.Debtor != nil {
			counterparty = strings.TrimSpace(transaction.Debtor.Name)
		} else if transaction.Creditor != nil {
			counterparty = strings.TrimSpace(transaction.Creditor.Name)
		}
		counterpartyRemittance := -1
		if counterparty == "" {
			for i, information := range transaction.RemittanceInformation {
				if information = strings.TrimSpace(information); information != "" {
					counterparty = information
					counterpartyRemittance = i
					break
				}
			}
		}
		noteParts := make([]string, 0, len(transaction.RemittanceInformation)+1)
		seenText := map[string]bool{}
		if counterparty != "" {
			seenText[strings.ToLower(strings.Join(strings.Fields(counterparty), " "))] = true
		}
		addNote := func(note string) {
			note = strings.TrimSpace(note)
			key := strings.ToLower(strings.Join(strings.Fields(note), " "))
			if key != "" && !seenText[key] {
				noteParts = append(noteParts, note)
				seenText[key] = true
			}
		}
		addNote(transaction.Note)
		for i, information := range transaction.RemittanceInformation {
			if i != counterpartyRemittance {
				addNote(information)
			}
		}
		rows = append(rows, data.ParsedRow{
			OccurredOn: occurredOn, Amount: amount, Currency: currency,
			Counterparty: counterparty, Note: strings.Join(noteParts, "\n"),
		})
	}
	return rows, rowErrors
}
