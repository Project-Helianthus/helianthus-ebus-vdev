package datasource

// ZoneSource provides live temperature and humidity data for a zone.
type ZoneSource interface {
	// Temperature returns the current temperature in °C, or an error if unavailable.
	Temperature() (float64, error)

	// Humidity returns the current relative humidity in %, or an error if unavailable.
	Humidity() (float64, error)
}
