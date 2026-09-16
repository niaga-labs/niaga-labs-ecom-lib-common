// Package bizday says what "a day" means for the business: a calendar day in
// the store's own time zone, not in UTC.
//
// niaga_db's session time zone is UTC and every timestamp column is
// timestamptz, so DATE(created_at), date_trunc('day', created_at) and
// CURRENT_DATE all cut days at 00:00 UTC -- 08:00 in Malaysia. An order placed
// at 00:30 MYT was charted on the previous day (NIAGA-302). The fix is to
// convert to the business zone inside each query, not to change the session
// time zone, which would silently change the meaning of every other query in
// every service.
//
// The zone comes from BUSINESS_TIMEZONE and defaults to Asia/Kuala_Lumpur. It
// is written into SQL as a literal (Postgres cannot bind a parameter inside
// GROUP BY portably), so a zone name is accepted only if it matches a strict
// IANA-name pattern AND time.LoadLocation knows it.
package bizday

import (
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	// Embedded zone data, so a scratch or alpine image without
	// /usr/share/zoneinfo still resolves Asia/Kuala_Lumpur.
	_ "time/tzdata"
)

// DefaultZone is the business time zone when BUSINESS_TIMEZONE is unset.
const DefaultZone = "Asia/Kuala_Lumpur"

// EnvVar names the environment variable that overrides DefaultZone.
const EnvVar = "BUSINESS_TIMEZONE"

// zoneName allows IANA names such as Asia/Kuala_Lumpur, Etc/GMT-8 or UTC, and
// nothing that could close a SQL string literal.
var zoneName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+)*$`)

// Zone is a validated business time zone.
type Zone struct {
	name string
	loc  *time.Location
}

// Load validates name and returns its Zone. An empty name means DefaultZone.
func Load(name string) (Zone, error) {
	if name == "" {
		name = DefaultZone
	}
	if !zoneName.MatchString(name) {
		return Zone{}, fmt.Errorf("bizday: %q is not a time zone name", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return Zone{}, fmt.Errorf("bizday: unknown time zone %q: %w", name, err)
	}
	return Zone{name: name, loc: loc}, nil
}

var (
	currentOnce sync.Once
	current     Zone
	currentErr  error
)

// Current returns the zone from BUSINESS_TIMEZONE, read once per process. If
// the variable holds something Load refuses, Current returns DefaultZone and
// ConfigError reports why, so a service can say so at boot instead of charting
// days in a zone nobody chose.
func Current() Zone {
	currentOnce.Do(func() {
		current, currentErr = resolve(os.Getenv(EnvVar))
	})
	return current
}

// resolve is Current's rule: the configured zone, or DefaultZone plus the
// reason the configured one was refused.
func resolve(configured string) (Zone, error) {
	z, err := Load(configured)
	if err != nil {
		fallback, _ := Load(DefaultZone)
		return fallback, err
	}
	return z, nil
}

// ConfigError is the reason BUSINESS_TIMEZONE was ignored, or nil.
func ConfigError() error {
	Current()
	return currentErr
}

// Name is the IANA name, e.g. Asia/Kuala_Lumpur.
func (z Zone) Name() string { return z.name }

// Location is the zone for Go-side date arithmetic.
func (z Zone) Location() *time.Location { return z.loc }

// ParseDate reads a YYYY-MM-DD date as midnight in the business zone, so a
// report for "2026-09-16" starts at 00:00 MYT rather than 00:00 UTC.
func (z Zone) ParseDate(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, z.loc)
}

// StartOfDay is midnight, in the business zone, of the day t falls on there.
func (z Zone) StartOfDay(t time.Time) time.Time {
	l := t.In(z.loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, z.loc)
}

// local renders "<col> AT TIME ZONE '<zone>'": the timestamptz column as a
// wall-clock timestamp in the business zone. col must be a trusted column
// expression written in code, never user input.
func (z Zone) local(col string) string {
	return fmt.Sprintf("(%s AT TIME ZONE '%s')", col, z.name)
}

// SQLDate is the business-zone calendar date of col, a replacement for
// DATE(col).
func (z Zone) SQLDate(col string) string {
	return z.local(col) + "::date"
}

// SQLTrunc truncates col to unit (day, week or month) in the business zone,
// a replacement for date_trunc(unit, col). The result is a timestamp without
// time zone holding the business-zone wall clock. An unknown unit panics:
// units are constants in code, so a bad one is a programming error.
func (z Zone) SQLTrunc(unit, col string) string {
	switch unit {
	case "day", "week", "month", "year":
	default:
		panic(fmt.Sprintf("bizday: unsupported date_trunc unit %q", unit))
	}
	return fmt.Sprintf("date_trunc('%s', %s)", unit, z.local(col))
}

// SQLStartOf is the timestamptz at which the current unit (day, week or
// month) began in the business zone. Compare a timestamptz column with it:
// "created_at >= " + z.SQLStartOf("day") replaces "created_at >= CURRENT_DATE".
func (z Zone) SQLStartOf(unit string) string {
	return fmt.Sprintf("(%s AT TIME ZONE '%s')", z.SQLTrunc(unit, "now()"), z.name)
}
