package jot

import (
	"github.com/jackstrohm/jot/pkg/utils"
)

// ConvertUnits converts between common units. Re-exported from utils.
func ConvertUnits(value float64, fromUnit, toUnit string) (string, error) {
	return utils.ConvertUnits(value, fromUnit, toUnit)
}

// ConvertTemperature converts temperature between C, F, K. Re-exported from utils.
func ConvertTemperature(value float64, from, to string) (float64, error) {
	return utils.ConvertTemperature(value, from, to)
}

// ConvertTimezone converts a time from one timezone to another. Re-exported from utils.
func ConvertTimezone(timeStr, fromTZ, toTZ string) (string, error) {
	return utils.ConvertTimezone(timeStr, fromTZ, toTZ)
}
