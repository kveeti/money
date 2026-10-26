package main

import (
	"log/slog"
	"money/backend/config"
	"os"
	_ "time/tzdata" // embed the IANA tz database for date filters and stats
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	config, err := config.LoadConfig()
	if err != nil {
		panic(err)
	}
	slog.Info("loaded config", "config", config)

	App(config, nil)

	slog.Info("bye!")
}
