package data

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGenericCSVDateAndTimestampRows(t *testing.T) {
	input := `date,occurred_at,amount,currency,counterparty,note
2026-07-01,,-12.34,EUR,Cafe,Lunch
,2026-07-01T21:30:00Z,10.00,EUR,Friend,Refund
2026-07-03,2026-07-03T01:30:00+00:00,-5.00,EUR,Shop,
`
	parser := NewGenericCSVParser(strings.NewReader(input), map[string]int{"EUR": 2})
	var rows []ParsedRow
	for parser.Next() {
		rows = append(rows, parser.Row())
	}
	require.NoError(t, parser.Err())
	require.Empty(t, parser.Errors())
	require.Len(t, rows, 3)

	require.Equal(t, "2026-07-01", rows[0].OccurredOn.Format(time.DateOnly))
	require.Nil(t, rows[0].OccurredAt)
	require.Equal(t, int64(-1234), rows[0].Amount)
	require.Equal(t, "Lunch", rows[0].Note)

	require.Equal(t, "2026-07-01", rows[1].OccurredOn.Format(time.DateOnly))
	require.NotNil(t, rows[1].OccurredAt)
	require.Equal(t, "2026-07-01T21:30:00Z", rows[1].OccurredAt.Format(time.RFC3339))

	// An explicit date remains authoritative while the exact instant is kept.
	require.Equal(t, "2026-07-03", rows[2].OccurredOn.Format(time.DateOnly))
	require.Equal(t, "2026-07-03T01:30:00Z", rows[2].OccurredAt.Format(time.RFC3339))
}

func TestGenericCSVCollectsInvalidRows(t *testing.T) {
	input := `date,occurred_at,amount,currency,counterparty,note
,,-1.00,EUR,Missing date,
2026-07-01,,0,EUR,Zero,
2026-07-02,,-2.00,EUR,Valid,
`
	parser := NewGenericCSVParser(strings.NewReader(input), map[string]int{"EUR": 2})
	require.True(t, parser.Next())
	require.Equal(t, "Valid", parser.Row().Counterparty)
	require.False(t, parser.Next())
	require.NoError(t, parser.Err())
	require.Len(t, parser.Errors(), 2)
	require.Equal(t, 2, parser.ErrorCount())
	require.Equal(t, 2, parser.Errors()[0].Line)
	require.Equal(t, 3, parser.Errors()[1].Line)
}

func TestGenericCSVRejectsNonUTCTimestamps(t *testing.T) {
	input := `date,occurred_at,amount,currency,counterparty,note
2026-07-01,2026-07-01 12:00:00,-1.00,EUR,No offset,
2026-07-01,2026-07-01T12:00:00+03:00,-1.00,EUR,Non-UTC offset,
`
	parser := NewGenericCSVParser(strings.NewReader(input), map[string]int{"EUR": 2})
	require.False(t, parser.Next())
	require.NoError(t, parser.Err())
	require.Len(t, parser.Errors(), 2)
	require.Equal(t, "invalid occurred_at: must be an RFC3339 UTC timestamp", parser.Errors()[0].Error)
	require.Equal(t, "invalid occurred_at: must be an RFC3339 UTC timestamp", parser.Errors()[1].Error)
}

func TestParseCanonicalAmountRejectsExcessPrecision(t *testing.T) {
	currencies := map[string]int{"EUR": 2, "JPY": 0}

	_, _, err := ParseCanonicalAmount("1.999", "EUR", currencies)
	require.EqualError(t, err, "amount has more than 2 decimal places")

	_, _, err = ParseCanonicalAmount("1.5", "JPY", currencies)
	require.EqualError(t, err, "amount has more than 0 decimal places")

	amount, currency, err := ParseCanonicalAmount("1.9", "EUR", currencies)
	require.NoError(t, err)
	require.Equal(t, int64(190), amount)
	require.Equal(t, "EUR", currency)
}

func TestGenericCSVBoundsStoredErrors(t *testing.T) {
	var input strings.Builder
	input.WriteString("date,occurred_at,amount,currency,counterparty,note\n")
	for range MaxStoredRowErrors + 5 {
		input.WriteString("bad,,-1,EUR,,\n")
	}

	parser := NewGenericCSVParser(strings.NewReader(input.String()), map[string]int{"EUR": 2})
	require.False(t, parser.Next())
	require.NoError(t, parser.Err())
	require.Len(t, parser.Errors(), MaxStoredRowErrors)
	require.Equal(t, MaxStoredRowErrors+5, parser.ErrorCount())
}

func TestGenericCSVRejectsTooManyRows(t *testing.T) {
	var input strings.Builder
	input.WriteString("date,occurred_at,amount,currency,counterparty,note\n")
	for range MaxGenericCSVRows + 1 {
		input.WriteString("2026-01-01,,-1,EUR,,\n")
	}

	parser := NewGenericCSVParser(strings.NewReader(input.String()), map[string]int{"EUR": 2})
	var rows int
	for parser.Next() {
		rows++
	}
	require.Equal(t, MaxGenericCSVRows, rows)
	require.EqualError(t, parser.Err(), "CSV has more than 100000 rows")
}

func TestGenericCSVRejectsOversizeText(t *testing.T) {
	columns := []string{"2026-01-01", "", "-1", "EUR", strings.Repeat("x", MaxImportTextBytes+1), ""}
	_, err := parseGenericCSVRow(columns, map[string]int{"EUR": 2})
	require.EqualError(t, err, "counterparty exceeds 4096 bytes")

	columns[4] = ""
	columns[5] = strings.Repeat("x", MaxImportTextBytes+1)
	_, err = parseGenericCSVRow(columns, map[string]int{"EUR": 2})
	require.EqualError(t, err, "note exceeds 4096 bytes")
}

func TestValidGenericCSVHeader(t *testing.T) {
	require.True(t, ValidGenericCSVHeader("date,occurred_at,amount,currency,counterparty,note\n"))
	require.True(t, ValidGenericCSVHeader("\ufeffDate,Occurred_At,Amount,Currency,Counterparty,Note\r\n"))
	require.False(t, ValidGenericCSVHeader("date,amount,counterparty\n"))
}
