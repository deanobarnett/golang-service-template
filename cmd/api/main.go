package main

import (
	"flag"
	"os"

	"example.com/pkg/database"
	"example.com/pkg/leveledlog"
	"example.com/pkg/server"
)

type config struct {
	addr  string
	env   string
	dbDSN string
}

type application struct {
	config config
	db     *database.Sqlite
	logger *leveledlog.Logger
}

func main() {
	var cfg config

	flag.StringVar(&cfg.addr, "addr", "localhost:4444", "server address to listen on")
	flag.StringVar(&cfg.env, "env", "development", "operating environment: development, testing, staging or production")
	flag.StringVar(&cfg.dbDSN, "dbdsn", "data/example.db", "sqlite3 DSN")
	flag.Parse()

	logger := leveledlog.NewLogger(os.Stdout, leveledlog.LevelAll, true)

	db, err := database.New(cfg.dbDSN)
	if err != nil {
		logger.Fatal(err)
	}
	defer db.Close()

	app := &application{
		config: cfg,
		db:     db,
		logger: logger,
	}

	logger.Info("starting server on %s", cfg.addr)

	err = server.Run(cfg.addr, app.routes())
	if err != nil {
		logger.Fatal(err)
	}

	logger.Info("server stopped")
}
