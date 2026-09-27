package outbox

import (
	"net/url"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NIAGA-565: the guard must refuse niaga_db whichever DSN form names it. It
// connects to the dev niaga_db and runs only SELECT current_database(); it
// never writes. Skips when the dev stack is not reachable.
func TestTheOutboxTestGuardRefusesNiagaDBInEveryDSNForm(t *testing.T) {
	host := os.Getenv("DB_HOST")
	if host == "" {
		host = "localhost"
	}
	pass := os.Getenv("DB_PASSWORD")
	if pass == "" {
		pass = "niaga_secret" // secret-scan: allow (the dev-infra default)
	}
	asURL := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("niaga", pass),
		Host:     host + ":5432",
		Path:     "/niaga_db",
		RawQuery: "sslmode=disable",
	}
	forms := map[string]string{
		"key=value": "host=" + host + " port=5432 user=niaga password=" + pass + " dbname=niaga_db sslmode=disable",
		"url":       asURL.String(),
	}
	for form, dsn := range forms {
		t.Run(form, func(t *testing.T) {
			db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Skipf("dev niaga_db not reachable: %v", err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Skipf("dev niaga_db not reachable: %v", err)
			}
			defer sqlDB.Close()
			if err := sqlDB.Ping(); err != nil {
				t.Skipf("dev niaga_db not reachable: %v", err)
			}
			if refuseSharedDB(db) == nil {
				t.Fatalf("the guard let the %s DSN for niaga_db through", form)
			}
		})
	}
}
