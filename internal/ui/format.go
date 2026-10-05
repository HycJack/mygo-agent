package ui

import "strconv"

// Duration renders a tool-call duration: milliseconds under a second,
// otherwise seconds with one decimal.
func Duration(ms int64) string {
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + "s"
}
