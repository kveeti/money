// Command gencsv generates large generic Money CSV files for import load tests.
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"
)

var currencies = []string{"EUR", "EUR", "EUR", "EUR", "USD", "GBP", "SEK"}
var counterparties = []string{
	"K-Market", "Prisma", "Lidl", "Alepa", "S-Market", "R-Kioski", "Wolt",
	"Spotify", "Netflix", "HSL", "Neste", "St1", "Amazon", "Verkkokauppa",
	"IKEA", "Restaurant", "McDonalds", "Pharmacy", "Stockmann", "Fazer",
}
var notes = []string{"", "", "", "Card payment", "Transfer", "Monthly fee", "Refund"}

func main() {
	n := flag.Int("n", 2_000_000, "number of rows")
	out := flag.String("o", "transactions.csv", "output file")
	dupRate := flag.Float64("dup", 0.05, "fraction of rows equal to the previous row")
	flag.Parse()

	file, err := os.Create(*out)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	buffer := bufio.NewWriterSize(file, 1<<20)
	defer buffer.Flush()
	writer := csv.NewWriter(buffer)
	defer writer.Flush()
	_ = writer.Write([]string{"date", "occurred_at", "amount", "currency", "counterparty", "note"})

	rng := rand.New(rand.NewSource(42))
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var previous []string
	for i := 0; i < *n; i++ {
		if previous != nil && rng.Float64() < *dupRate {
			_ = writer.Write(previous)
			continue
		}
		amount := fmt.Sprintf("-%d.%02d", rng.Intn(500), rng.Intn(100))
		if rng.Intn(10) == 0 {
			amount = amount[1:]
		}
		previous = []string{
			start.AddDate(0, 0, rng.Intn(2000)).Format(time.DateOnly), "", amount,
			currencies[rng.Intn(len(currencies))], counterparties[rng.Intn(len(counterparties))], notes[rng.Intn(len(notes))],
		}
		_ = writer.Write(previous)
	}
	fmt.Fprintf(os.Stderr, "wrote %d rows to %s\n", *n, *out)
}
