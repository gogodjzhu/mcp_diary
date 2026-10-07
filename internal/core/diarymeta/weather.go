package diarymeta

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	geocodingURL = "https://geocoding-api.open-meteo.com/v1/search"
	forecastURL  = "https://api.open-meteo.com/v1/forecast"
	archiveURL   = "https://archive-api.open-meteo.com/v1/archive"
)

// HTTPDoer is the subset of *http.Client used here, kept small for testing.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// WeatherClient resolves a place name and fetches the WMO weather code for a
// date from Open-Meteo, which needs no API key.
type WeatherClient struct {
	http HTTPDoer
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]coords
}

type coords struct {
	lat float64
	lon float64
}

// Weather is the resolved weather for a single day.
type Weather struct {
	Emoji string
	Code  int
}

// NewWeatherClient builds a client. A nil HTTP client gets a default with an
// 8 second timeout.
func NewWeatherClient(client HTTPDoer) *WeatherClient {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &WeatherClient{http: client, now: time.Now, cache: map[string]coords{}}
}

// Weather resolves location to coordinates and returns that day's weather.
func (c *WeatherClient) Weather(ctx context.Context, location, date string) (Weather, error) {
	lat, lon, err := c.geocode(ctx, location)
	if err != nil {
		return Weather{}, err
	}
	code, err := c.codeForDate(ctx, lat, lon, date)
	if err != nil {
		return Weather{}, err
	}
	return Weather{Emoji: weatherEmoji(code), Code: code}, nil
}

func (c *WeatherClient) geocode(ctx context.Context, location string) (float64, float64, error) {
	key := strings.ToLower(strings.TrimSpace(location))
	c.mu.Lock()
	pt, ok := c.cache[key]
	c.mu.Unlock()
	if ok {
		return pt.lat, pt.lon, nil
	}

	q := url.Values{}
	q.Set("name", location)
	q.Set("count", "1")
	q.Set("language", "zh")
	q.Set("format", "json")
	var out struct {
		Results []struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := c.getJSON(ctx, geocodingURL+"?"+q.Encode(), &out); err != nil {
		return 0, 0, err
	}
	if len(out.Results) == 0 {
		return 0, 0, fmt.Errorf("location %q not found", location)
	}
	pt = coords{lat: out.Results[0].Latitude, lon: out.Results[0].Longitude}
	c.mu.Lock()
	c.cache[key] = pt
	c.mu.Unlock()
	return pt.lat, pt.lon, nil
}

func (c *WeatherClient) codeForDate(ctx context.Context, lat, lon float64, date string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// The forecast endpoint covers roughly the last three months and the near
	// future; the archive endpoint covers everything older.
	if c.recent(date) {
		if code, err := c.query(ctx, forecastURL, lat, lon, date); err == nil {
			return code, nil
		}
	}
	return c.query(ctx, archiveURL, lat, lon, date)
}

func (c *WeatherClient) recent(date string) bool {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return false
	}
	days := int(c.now().Sub(t).Hours() / 24)
	return days >= -16 && days <= 92
}

func (c *WeatherClient) query(ctx context.Context, base string, lat, lon float64, date string) (int, error) {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(lat, 'f', -1, 64))
	q.Set("longitude", strconv.FormatFloat(lon, 'f', -1, 64))
	q.Set("daily", "weather_code")
	q.Set("timezone", "auto")
	q.Set("start_date", date)
	q.Set("end_date", date)
	var out struct {
		Daily struct {
			WeatherCode []int `json:"weather_code"`
		} `json:"daily"`
	}
	if err := c.getJSON(ctx, base+"?"+q.Encode(), &out); err != nil {
		return 0, err
	}
	if len(out.Daily.WeatherCode) == 0 {
		return 0, fmt.Errorf("no weather for %s", date)
	}
	return out.Daily.WeatherCode[0], nil
}

func (c *WeatherClient) getJSON(ctx context.Context, rawURL string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mcp-diary")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("open-meteo %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

// weatherEmoji maps a WMO weather interpretation code to an emoji.
func weatherEmoji(code int) string {
	switch {
	case code == 0:
		return "☀️"
	case code == 1:
		return "🌤️"
	case code == 2:
		return "⛅"
	case code == 3:
		return "☁️"
	case code == 45 || code == 48:
		return "🌫️"
	case code >= 51 && code <= 57:
		return "🌦️"
	case code >= 61 && code <= 67:
		return "🌧️"
	case code >= 71 && code <= 77:
		return "❄️"
	case code >= 80 && code <= 82:
		return "🌦️"
	case code == 85 || code == 86:
		return "🌨️"
	case code >= 95:
		return "⛈️"
	default:
		return "🌡️"
	}
}
