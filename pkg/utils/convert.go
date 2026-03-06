package utils

import (
	"fmt"
	"strings"
	"time"
)

var timezoneAliases = map[string]string{
	"pst": "America/Los_Angeles", "pt": "America/Los_Angeles", "pacific": "America/Los_Angeles",
	"pdt": "America/Los_Angeles", "los angeles": "America/Los_Angeles", "la": "America/Los_Angeles",
	"mst": "America/Denver", "mt": "America/Denver", "mountain": "America/Denver",
	"mdt": "America/Denver", "denver": "America/Denver",
	"cst": "America/Chicago", "ct": "America/Chicago", "central": "America/Chicago",
	"cdt": "America/Chicago", "chicago": "America/Chicago",
	"est": "America/New_York", "et": "America/New_York", "eastern": "America/New_York",
	"edt": "America/New_York", "new york": "America/New_York", "nyc": "America/New_York",
	"utc": "UTC", "gmt": "UTC", "z": "UTC",
	"jst": "Asia/Tokyo", "tokyo": "Asia/Tokyo", "japan": "Asia/Tokyo",
	"kst": "Asia/Seoul", "seoul": "Asia/Seoul", "korea": "Asia/Seoul",
	"cet": "Europe/Paris", "paris": "Europe/Paris",
	"bst": "Europe/London", "london": "Europe/London", "uk": "Europe/London",
	"ist": "Asia/Kolkata", "india": "Asia/Kolkata",
	"aest": "Australia/Sydney", "sydney": "Australia/Sydney",
	"aedt": "Australia/Sydney",
	"cst_china": "Asia/Shanghai", "shanghai": "Asia/Shanghai", "beijing": "Asia/Shanghai", "china": "Asia/Shanghai",
	"hkt": "Asia/Hong_Kong", "hong kong": "Asia/Hong_Kong",
	"sgt": "Asia/Singapore", "singapore": "Asia/Singapore",
}

// ConvertTimezone converts a time from one timezone to another.
func ConvertTimezone(timeStr, fromTZ, toTZ string) (string, error) {
	fromTZ = resolveTimezone(fromTZ)
	toTZ = resolveTimezone(toTZ)

	fromLoc, err := time.LoadLocation(fromTZ)
	if err != nil {
		return "", fmt.Errorf("unknown source timezone: %s", fromTZ)
	}
	toLoc, err := time.LoadLocation(toTZ)
	if err != nil {
		return "", fmt.Errorf("unknown target timezone: %s", toTZ)
	}

	parsedTime, err := parseTimeString(timeStr)
	if err != nil {
		return "", err
	}

	now := time.Now()
	sourceTime := time.Date(now.Year(), now.Month(), now.Day(),
		parsedTime.Hour(), parsedTime.Minute(), 0, 0, fromLoc)

	targetTime := sourceTime.In(toLoc)

	return fmt.Sprintf("%s %s = %s %s",
		sourceTime.Format("3:04 PM"), fromTZ,
		targetTime.Format("3:04 PM"), toTZ), nil
}

func resolveTimezone(tz string) string {
	tz = strings.ToLower(strings.TrimSpace(tz))
	if alias, ok := timezoneAliases[tz]; ok {
		return alias
	}
	return tz
}

func parseTimeString(timeStr string) (time.Time, error) {
	timeStr = strings.TrimSpace(timeStr)
	formats := []string{
		"3:04 PM", "3:04PM", "3:04pm", "3:04 pm",
		"15:04", "3PM", "3pm", "3 PM", "3 pm",
		"15:04:05",
	}
	for _, format := range formats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("could not parse time: %s", timeStr)
}
