package diarymeta

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
)

// Enricher derives per-entry metadata from the workspace settings. It never
// fails a diary write: any lookup error just leaves that field empty.
type Enricher struct {
	settings *Service
	weather  *WeatherClient
	now      func() time.Time
	logger   *slog.Logger
}

// NewEnricher builds an enricher.
func NewEnricher(settings *Service, weather *WeatherClient, logger *slog.Logger) *Enricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Enricher{settings: settings, weather: weather, now: time.Now, logger: logger}
}

// Enrich implements diary.MetaEnricher.
func (e *Enricher) Enrich(ctx context.Context, fs *filesystem.Service, date string) diary.EntryMeta {
	meta := diary.EntryMeta{}
	if weekday, ok := WeekdayCN(date); ok {
		meta.Weekday = weekday
	}
	settings, err := e.settings.Get(ctx, fs)
	if err != nil {
		e.logger.Warn("read diary metadata settings", "err", err)
		return meta
	}
	if settings.LunarEnabled {
		if lunar, ok := LunarDate(date); ok {
			meta.Lunar = lunar
		}
	}
	if settings.WeatherEnabled {
		location := strings.TrimSpace(settings.WeatherLocation)
		if location == "" {
			e.logger.Warn("weather metadata enabled without a location")
			return meta
		}
		if w, ok := e.fetchWeather(ctx, location, date); ok {
			meta.Weather = w.Emoji
			meta.WeatherCode = w.Code
			meta.Location = location
			meta.Source = "open-meteo"
			meta.FetchedAt = e.now().UTC()
		}
	}
	return meta
}

// fetchWeather retries a transient Open-Meteo failure. Each attempt gets its
// own deadline, so one slow attempt cannot eat the next one's budget. A lookup
// that still fails leaves the weather field empty; it never fails the write.
func (e *Enricher) fetchWeather(ctx context.Context, location, date string) (Weather, bool) {
	for attempt := 1; attempt <= Attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Weather{}, false
		}
		wctx, cancel := context.WithTimeout(ctx, AttemptTimeout)
		w, err := e.weather.Weather(wctx, location, date)
		cancel()
		if err == nil {
			return w, true
		}
		e.logger.Warn("fetch weather metadata", "location", location, "date", date, "attempt", attempt, "err", err)
	}
	return Weather{}, false
}
