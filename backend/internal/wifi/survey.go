package wifi

import (
	"strconv"
	"strings"
)

// ParseSurvey returns channel busy fractions only for complete iw survey blocks.
func ParseSurvey(raw string) map[int]float64 {
	result := map[int]float64{}
	if len(raw) > 1<<20 {
		return result
	}
	channel := 0
	sawBusy := false
	active, busy := uint64(0), uint64(0)
	flush := func() {
		if channel > 0 && active > 0 && sawBusy && busy <= active {
			result[channel] = float64(busy) / float64(active)
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "frequency:") {
			flush()
			channel = 0
			active = 0
			busy = 0
			sawBusy = false
			fields := strings.Fields(line)
			if len(fields) > 1 {
				mhz, _ := strconv.Atoi(fields[1])
				if mhz >= 2412 && mhz <= 2462 {
					channel = (mhz - 2407) / 5
				} else if mhz >= 5000 {
					channel = (mhz - 5000) / 5
				}
			}
		}
		fields := strings.Fields(line)
		if len(fields) > 3 && strings.HasPrefix(line, "channel active time:") {
			active, _ = strconv.ParseUint(fields[3], 10, 64)
		}
		if len(fields) > 3 && strings.HasPrefix(line, "channel busy time:") {
			var err error
			busy, err = strconv.ParseUint(fields[3], 10, 64)
			sawBusy = err == nil
		}
	}
	flush()
	return result
}
