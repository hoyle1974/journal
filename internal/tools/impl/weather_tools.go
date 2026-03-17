package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/tools"
)

type getWeatherArgs struct {
	Location string `json:"location" description:"City name or location (e.g. 'Chicago', 'London, UK')" required:"true"`
	Unit     string `json:"unit" description:"Temperature unit" enum:"celsius,fahrenheit" default:"fahrenheit"`
	Days     int    `json:"days" description:"Number of forecast days (1–7, default 3)" default:"3"`
}

func init() {
	registerWeatherTools()
}

func registerWeatherTools() {
	tools.Register(&tools.Tool{
		Name:        "get_weather",
		Description: "Get current weather conditions and a short forecast for any city or location. Use this when the user asks about weather, wants to plan an outdoor activity, or asks if they need an umbrella.",
		Category:    "web",
		DocURL:      "https://open-meteo.com/",
		Args:        &getWeatherArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getWeatherArgs)
			if a.Location == "" {
				return tools.MissingParam("location")
			}
			days := a.Days
			if days <= 0 {
				days = 3
			}
			if days > 7 {
				days = 7
			}
			unit := a.Unit
			if unit == "" {
				unit = "fahrenheit"
			}
			result, err := fetchWeather(ctx, a.Location, unit, days)
			if err != nil {
				return tools.Fail("Weather error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})
}

// geocodeLocation resolves a location name to lat/lng using Open-Meteo's geocoding API.
func geocodeLocation(ctx context.Context, location string) (lat, lng float64, resolvedName string, err error) {
	apiURL := "https://geocoding-api.open-meteo.com/v1/search?name=" + url.QueryEscape(location) + "&count=1&language=en&format=json"

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return 0, 0, "", fmt.Errorf("geocode request: %w", err)
	}
	req.Header.Set("User-Agent", "JotPersonalAssistant/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, "", fmt.Errorf("geocode fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, "", fmt.Errorf("geocode HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0, 0, "", fmt.Errorf("geocode read: %w", err)
	}

	var geo struct {
		Results []struct {
			Name      string  `json:"name"`
			Country   string  `json:"country"`
			Admin1    string  `json:"admin1"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &geo); err != nil {
		return 0, 0, "", fmt.Errorf("geocode parse: %w", err)
	}
	if len(geo.Results) == 0 {
		return 0, 0, "", fmt.Errorf("location not found: %q", location)
	}

	r := geo.Results[0]
	parts := []string{r.Name}
	if r.Admin1 != "" {
		parts = append(parts, r.Admin1)
	}
	if r.Country != "" {
		parts = append(parts, r.Country)
	}
	return r.Latitude, r.Longitude, strings.Join(parts, ", "), nil
}

// wmoDescription maps WMO weather interpretation codes to human-readable strings.
func wmoDescription(code int) string {
	switch {
	case code == 0:
		return "Clear sky"
	case code == 1:
		return "Mainly clear"
	case code == 2:
		return "Partly cloudy"
	case code == 3:
		return "Overcast"
	case code >= 45 && code <= 48:
		return "Foggy"
	case code >= 51 && code <= 55:
		return "Drizzle"
	case code >= 56 && code <= 57:
		return "Freezing drizzle"
	case code >= 61 && code <= 65:
		return "Rain"
	case code >= 66 && code <= 67:
		return "Freezing rain"
	case code >= 71 && code <= 77:
		return "Snow"
	case code >= 80 && code <= 82:
		return "Rain showers"
	case code >= 85 && code <= 86:
		return "Snow showers"
	case code == 95:
		return "Thunderstorm"
	case code >= 96 && code <= 99:
		return "Thunderstorm with hail"
	default:
		return "Unknown"
	}
}

// fetchWeather retrieves current conditions and a daily forecast via Open-Meteo.
func fetchWeather(ctx context.Context, location, unit string, days int) (string, error) {
	lat, lng, resolvedName, err := geocodeLocation(ctx, location)
	if err != nil {
		return "", err
	}

	tempUnit := "fahrenheit"
	tempSymbol := "°F"
	windUnit := "mph"
	if unit == "celsius" {
		tempUnit = "celsius"
		tempSymbol = "°C"
		windUnit = "kmh"
	}

	apiURL := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%f&longitude=%f"+
			"&current=temperature_2m,apparent_temperature,weather_code,wind_speed_10m,relative_humidity_2m"+
			"&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max"+
			"&temperature_unit=%s&wind_speed_unit=%s&forecast_days=%d&timezone=auto",
		lat, lng, tempUnit, windUnit, days,
	)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("weather request: %w", err)
	}
	req.Header.Set("User-Agent", "JotPersonalAssistant/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("weather fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("weather HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("weather read: %w", err)
	}

	var w struct {
		Current struct {
			Temperature  float64 `json:"temperature_2m"`
			ApparentTemp float64 `json:"apparent_temperature"`
			WeatherCode  int     `json:"weather_code"`
			WindSpeed    float64 `json:"wind_speed_10m"`
			Humidity     int     `json:"relative_humidity_2m"`
		} `json:"current"`
		Daily struct {
			Time        []string  `json:"time"`
			WeatherCode []int     `json:"weather_code"`
			TempMax     []float64 `json:"temperature_2m_max"`
			TempMin     []float64 `json:"temperature_2m_min"`
			PrecipProb  []int     `json:"precipitation_probability_max"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return "", fmt.Errorf("weather parse: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Weather for %s\n\n", resolvedName)
	fmt.Fprintf(&sb, "Current: %s\n", wmoDescription(w.Current.WeatherCode))
	fmt.Fprintf(&sb, "Temperature: %.1f%s (feels like %.1f%s)\n",
		w.Current.Temperature, tempSymbol, w.Current.ApparentTemp, tempSymbol)
	fmt.Fprintf(&sb, "Wind: %.1f %s  |  Humidity: %d%%\n\n", w.Current.WindSpeed, windUnit, w.Current.Humidity)

	if len(w.Daily.Time) > 0 {
		fmt.Fprintf(&sb, "Forecast:\n")
		for i, date := range w.Daily.Time {
			if i >= len(w.Daily.WeatherCode) {
				break
			}
			precip := 0
			if i < len(w.Daily.PrecipProb) {
				precip = w.Daily.PrecipProb[i]
			}
			fmt.Fprintf(&sb, "  %s: %s, High %.1f%s / Low %.1f%s, Precip %d%%\n",
				date,
				wmoDescription(w.Daily.WeatherCode[i]),
				w.Daily.TempMax[i], tempSymbol,
				w.Daily.TempMin[i], tempSymbol,
				precip,
			)
		}
	}

	return sb.String(), nil
}
