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
		wctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if w, err := e.weather.Weather(wctx, location, date); err == nil {
			meta.Weather = w.Emoji
			meta.WeatherCode = w.Code
			meta.Location = location
			meta.Source = "open-meteo"
			meta.FetchedAt = e.now().UTC()
		} else {
			e.logger.Warn("fetch weather metadata", "location", location, "date", date, "err", err)
		}
	}
	return meta
}
