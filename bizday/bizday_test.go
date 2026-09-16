package bizday

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAcceptsRealZonesAndDefaultsEmpty(t *testing.T) {
	for _, name := range []string{"", "Asia/Kuala_Lumpur", "UTC", "Etc/GMT-8", "America/Argentina/Buenos_Aires"} {
		z, err := Load(name)
		if err != nil {
			t.Fatalf("Load(%q): %v", name, err)
		}
		want := name
		if want == "" {
			want = DefaultZone
		}
		if z.Name() != want {
			t.Fatalf("Load(%q).Name() = %q, want %q", name, z.Name(), want)
		}
	}
}

func TestLoadRefusesAnythingThatCouldEscapeSQL(t *testing.T) {
	for _, name := range []string{
		"Asia/Kuala_Lumpur'; DROP TABLE x; --",
		"UTC'",
		"Asia Kuala_Lumpur",
		"../etc/passwd",
		"Mars/Olympus_Mons",
		"/Asia/Kuala_Lumpur",
	} {
		if _, err := Load(name); err == nil {
			t.Fatalf("Load(%q) accepted", name)
		}
	}
}

func TestAnOrderAtHalfPastMidnightMYTIsOnTheMalaysianDate(t *testing.T) {
	z, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-09-15 16:30 UTC is 2026-09-16 00:30 in Kuala Lumpur.
	placed := time.Date(2026, 9, 15, 16, 30, 0, 0, time.UTC)
	start := z.StartOfDay(placed)
	if got := start.Format("2006-01-02 15:04 -0700"); got != "2026-09-16 00:00 +0800" {
		t.Fatalf("StartOfDay = %s", got)
	}
	if !start.Equal(time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("StartOfDay in UTC = %s, want 2026-09-15 16:00", start.UTC())
	}
}

func TestParseDateIsMidnightInTheBusinessZone(t *testing.T) {
	z, _ := Load("Asia/Kuala_Lumpur")
	d, err := z.ParseDate("2026-09-16")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Equal(time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("ParseDate = %s (UTC %s)", d, d.UTC())
	}
	if _, err := z.ParseDate("16/09/2026"); err == nil {
		t.Fatal("a non-ISO date was accepted")
	}
}

func TestSQLFragments(t *testing.T) {
	z, _ := Load("Asia/Kuala_Lumpur")
	cases := map[string]string{
		z.SQLDate("created_at"):            "(created_at AT TIME ZONE 'Asia/Kuala_Lumpur')::date",
		z.SQLTrunc("week", "o.created_at"): "date_trunc('week', (o.created_at AT TIME ZONE 'Asia/Kuala_Lumpur'))",
		z.SQLStartOf("day"):                "(date_trunc('day', (now() AT TIME ZONE 'Asia/Kuala_Lumpur')) AT TIME ZONE 'Asia/Kuala_Lumpur')",
		z.SQLStartOf("month"):              "(date_trunc('month', (now() AT TIME ZONE 'Asia/Kuala_Lumpur')) AT TIME ZONE 'Asia/Kuala_Lumpur')",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	}
}

func TestSQLTruncRefusesAnUnknownUnit(t *testing.T) {
	z, _ := Load("")
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "unsupported") {
			t.Fatalf("recover() = %v", r)
		}
	}()
	z.SQLTrunc("hour'); --", "created_at")
}

func TestAConfiguredZoneIsUsedAndABadOneFallsBackLoudly(t *testing.T) {
	z, err := resolve("UTC")
	if err != nil || z.Name() != "UTC" {
		t.Fatalf("resolve(UTC) = %q, %v", z.Name(), err)
	}
	z, err = resolve("")
	if err != nil || z.Name() != DefaultZone {
		t.Fatalf("resolve(\"\") = %q, %v", z.Name(), err)
	}
	z, err = resolve("Nowhere/Land")
	if err == nil {
		t.Fatal("an unknown zone was not reported")
	}
	if z.Name() != DefaultZone {
		t.Fatalf("fallback = %q, want %q", z.Name(), DefaultZone)
	}
}

func TestCurrentIsTheDefaultWhenUnset(t *testing.T) {
	if got := Current().Name(); got != DefaultZone && ConfigError() == nil {
		t.Fatalf("Current() = %q with no error", got)
	}
}
