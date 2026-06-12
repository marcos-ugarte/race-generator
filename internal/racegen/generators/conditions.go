package generators

import (
	"strconv"

	"vg-racegen/internal/racegen/config"
	"vg-racegen/internal/racegen/rng"
)

// windDirections is the 16-point compass used by the legacy generator
// (conditions.ts:11-16). Order is significant for byte-for-byte parity.
var windDirections = [16]string{
	"N", "NNE", "NE", "ENE",
	"E", "ESE", "SE", "SSE",
	"S", "SSW", "SW", "WSW",
	"W", "WNW", "NW", "NNW",
}

// Conditions is the environmental block for one race. Mirrors the legacy
// Conditions interface (conditions.ts:18-24).
type Conditions struct {
	CourseConditions string `json:"courseConditions"`
	Weather          string `json:"weather"`
	Temperature      int    `json:"temperature"`
	Humidity         int    `json:"humidity"`
	Wind             string `json:"wind"`
}

// GenerateConditions returns a fresh Conditions block. Sequence of RNG
// calls is fixed (weather, wind dir, wind speed, temperature, humidity)
// to match conditions.ts:36-50 and keep replay parity with the legacy
// engine.
func GenerateConditions(mt rng.Source, cfg config.GameTypeConfigExt) Conditions {
	weather := weightedWeather(mt, cfg)
	windDir := windDirections[rng.CertifiedInt(mt, 0, len(windDirections)-1)]
	windSpeed := rng.CertifiedInt(mt, cfg.WindSpeedRange.Min, cfg.WindSpeedRange.Max)
	temperature := rng.CertifiedInt(mt, cfg.TemperatureRange.Min, cfg.TemperatureRange.Max)
	humidity := rng.CertifiedInt(mt, cfg.HumidityRange.Min, cfg.HumidityRange.Max)

	return Conditions{
		CourseConditions: "fast",
		Weather:          weather,
		Temperature:      temperature,
		Humidity:         humidity,
		Wind:             strconv.Itoa(windSpeed) + " " + windDir,
	}
}

// weightedWeather draws a weather option using the cumulative-cutoff
// algorithm from conditions.ts:26-34. WeatherWeights are validated to
// sum within [0.999, 1.001] at config init; small residual error is
// absorbed by returning the last option as fallback.
func weightedWeather(mt rng.Source, cfg config.GameTypeConfigExt) string {
	r := rng.CertifiedFloat(mt)
	var cum float64
	for i, w := range cfg.WeatherWeights {
		cum += w
		if r < cum {
			return cfg.WeatherOptions[i]
		}
	}
	return cfg.WeatherOptions[len(cfg.WeatherOptions)-1]
}
