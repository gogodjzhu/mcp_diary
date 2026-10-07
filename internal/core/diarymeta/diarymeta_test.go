package diarymeta

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

func testFS(t *testing.T) *filesystem.Service {
	t.Helper()
	fs, err := filesystem.New(filesystem.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestLunarAndWeekday(t *testing.T) {
	lunar, ok := LunarDate("2026-09-30")
	if !ok || lunar != "八月二十" {
		t.Fatalf("lunar = %q ok=%v", lunar, ok)
	}
	weekday, ok := WeekdayCN("2026-09-30")
	if !ok || weekday != "三" {
		t.Fatalf("weekday = %q ok=%v", weekday, ok)
	}
	if _, ok := LunarDate("not-a-date"); ok {
		t.Fatal("invalid date should fail")
	}
}

func TestWeatherEmoji(t *testing.T) {
	cases := map[int]string{
		0:  "☀️",
		2:  "⛅",
		45: "🌫️",
		61: "🌧️",
		75: "❄️",
		95: "⛈️",
	}
	for code, want := range cases {
		if got := weatherEmoji(code); got != want {
			t.Fatalf("code %d = %q, want %q", code, got, want)
		}
	}
}

type fakeDoer struct {
	geocoding int
	weather   int
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	var body string
	switch {
	case strings.Contains(host, "geocoding"):
		f.geocoding++
		body = `{"results":[{"latitude":39.9,"longitude":116.4}]}`
	case strings.Contains(host, "forecast") || strings.Contains(host, "archive"):
		f.weather++
		body = `{"daily":{"weather_code":[61]}}`
	default:
		body = `{}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestWeatherClientResolvesAndCachesLocation(t *testing.T) {
	doer := &fakeDoer{}
	client := NewWeatherClient(doer)
	// A historical date forces the archive endpoint, whose result is canned.
	got, err := client.Weather(context.Background(), "Beijing", "2000-01-15")
	if err != nil {
		t.Fatalf("Weather: %v", err)
	}
	if got.Code != 61 || got.Emoji != "🌧️" {
		t.Fatalf("weather = %+v", got)
	}
	if doer.geocoding != 1 || doer.weather != 1 {
		t.Fatalf("calls = geocoding %d weather %d", doer.geocoding, doer.weather)
	}
	if _, err := client.Weather(context.Background(), "beijing", "2000-01-16"); err != nil {
		t.Fatalf("second Weather: %v", err)
	}
	if doer.geocoding != 1 {
		t.Fatalf("geocoding should be cached, calls = %d", doer.geocoding)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	svc := New()
	fs := testFS(t)
	ctx := context.Background()

	got, err := svc.Get(ctx, fs)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LunarEnabled || got.WeatherEnabled {
		t.Fatalf("default settings = %+v", got)
	}

	want := Settings{LunarEnabled: true, WeatherEnabled: true, WeatherLocation: "Beijing"}
	if _, err := svc.Save(ctx, fs, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A fresh service must read the persisted value.
	reloaded := New()
	got, err = reloaded.Get(ctx, fs)
	if err != nil {
		t.Fatalf("reload Get: %v", err)
	}
	if got != want {
		t.Fatalf("reloaded = %+v, want %+v", got, want)
	}
}

func TestEnricherUsesSettings(t *testing.T) {
	settings := New()
	fs := testFS(t)
	ctx := context.Background()

	// Disabled: only the weekday is derived (no network).
	enricher := NewEnricher(settings, NewWeatherClient(&fakeDoer{}), nil)
	meta := enricher.Enrich(ctx, fs, "2026-09-30")
	if meta.Lunar != "" || meta.Weather != "" {
		t.Fatalf("disabled meta = %+v", meta)
	}
	if meta.Weekday != "三" {
		t.Fatalf("weekday = %q", meta.Weekday)
	}

	if _, err := settings.Save(ctx, fs, Settings{LunarEnabled: true}); err != nil {
		t.Fatal(err)
	}
	meta = enricher.Enrich(ctx, fs, "2026-09-30")
	if meta.Lunar != "八月二十" {
		t.Fatalf("lunar = %q", meta.Lunar)
	}
}
