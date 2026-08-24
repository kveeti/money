// Command convertcsv converts OP transaction CSV files to Money's generic CSV format.
package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var sourceHeader = []string{
	"Kirjauspäivä",
	"Arvopäivä",
	"Määrä EUROA",
	"Laji",
	"Selitys",
	"Saaja/Maksaja",
	"Saajan tilinumero",
	"Saajan pankin BIC",
	"Viite",
	"Viesti",
	"Arkistointitunnus",
}

var amountPattern = regexp.MustCompile(`^-?\d+,\d{2}$`)

func main() {
	inPath := flag.String("in", "", "input OP CSV path (required)")
	outPath := flag.String("out", "", "output Money CSV path (required)")
	flag.Parse()

	if err := run(*inPath, *outPath); err != nil {
		fmt.Fprintln(os.Stderr, "convertcsv:", err)
		os.Exit(1)
	}
}

func run(inPath, outPath string) error {
	if inPath == "" || outPath == "" {
		return errors.New("-in and -out are required")
	}
	if inPath == outPath {
		return errors.New("input and output paths must differ")
	}

	input, err := os.Open(inPath)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer input.Close()

	output, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	if err := convert(input, output); err != nil {
		output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	return nil
}

func convert(input io.Reader, output io.Writer) error {
	reader := csv.NewReader(input)
	reader.Comma = ';'
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	if !sameFields(header, sourceHeader) {
		return errors.New("input does not have the expected OP CSV header")
	}

	writer := csv.NewWriter(output)
	if err := writer.Write([]string{"date", "occurred_at", "amount", "currency", "counterparty", "note"}); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	for line := 2; ; line++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read line %d: %w", line, err)
		}
		if len(record) != len(sourceHeader) {
			return fmt.Errorf("line %d: expected %d columns, got %d", line, len(sourceHeader), len(record))
		}

		amount, err := normalizeAmount(record[2])
		if err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		note := joinNote(record[4], strings.TrimPrefix(strings.TrimSpace(record[8]), "ref="), record[9])
		if err := writer.Write([]string{
			strings.TrimSpace(record[0]),
			"",
			amount,
			"EUR",
			strings.TrimSpace(record[5]),
			note,
		}); err != nil {
			return fmt.Errorf("write line %d: %w", line, err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func normalizeAmount(raw string) (string, error) {
	amount := strings.TrimSpace(raw)
	if !amountPattern.MatchString(amount) {
		return "", fmt.Errorf("invalid amount %q", raw)
	}
	return strings.Replace(amount, ",", ".", 1), nil
}

func joinNote(parts ...string) string {
	note := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			note = append(note, part)
		}
	}
	return strings.Join(note, " | ")
}

func sameFields(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			return false
		}
	}
	return true
}
