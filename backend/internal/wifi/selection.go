package wifi

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type Neighbor struct {
	Channel   int
	SignalDBm int
}
type Selection struct {
	Channel  int     `json:"channel"`
	WidthMHz int     `json:"widthMHz"`
	Reason   string  `json:"reason"`
	Score    float64 `json:"score"`
}

// ParseScan accepts iw BSS records. Unknown or incomplete records contribute no score.
func ParseScan(raw string) []Neighbor {
	if len(raw) > 1<<20 {
		return nil
	}
	var result []Neighbor
	n := Neighbor{}
	valid := false
	flush := func() {
		if valid && n.Channel > 0 && n.SignalDBm < 0 {
			result = append(result, n)
		}
		n = Neighbor{}
		valid = false
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "BSS ") {
			flush()
			valid = true
		}
		if strings.HasPrefix(line, "DS Parameter set: channel ") {
			n.Channel, _ = strconv.Atoi(strings.TrimPrefix(line, "DS Parameter set: channel "))
		}
		if strings.HasPrefix(line, "primary channel: ") {
			n.Channel, _ = strconv.Atoi(strings.TrimPrefix(line, "primary channel: "))
		}
		if strings.HasPrefix(line, "freq: ") && n.Channel == 0 {
			mhz, _ := strconv.Atoi(strings.TrimPrefix(line, "freq: "))
			if mhz >= 2412 && mhz <= 2462 {
				n.Channel = (mhz - 2407) / 5
			} else if mhz >= 5000 {
				n.Channel = (mhz - 5000) / 5
			}
		}
		if strings.HasPrefix(line, "signal: ") {
			fields := strings.Fields(strings.TrimPrefix(line, "signal: "))
			if len(fields) == 0 {
				continue
			}
			f, err := strconv.ParseFloat(fields[0], 64)
			if err == nil {
				n.SignalDBm = int(f)
			}
		}
	}
	flush()
	return result
}

// SelectChannel uses the portable profile and the current PHY restrictions.
// A missing scan keeps a legal previous channel, then chooses a stable legal one.
func SelectChannel(ap config.WiFiAP, caps Capabilities, neighbors []Neighbor, busy map[int]float64, previous int) (Selection, error) {
	if ap.ChannelWidthMHz == config.WiFiAutoWidthMHz {
		for _, width := range []int{80, 40, 20} {
			effective := ap
			effective.ChannelWidthMHz = width
			selection, err := SelectChannel(effective, caps, neighbors, busy, previous)
			if err == nil {
				selection.WidthMHz = width
				return selection, nil
			}
		}
		return Selection{}, errors.New("no legal non-DFS AP channel and width")
	}

	band := ap.Band
	if band == "" {
		band = "2g"
	}
	candidates := []int{1, 6, 11, 2, 3, 4, 5, 7, 8, 9, 10}
	if band == "5g" {
		candidates = []int{36, 40, 44, 48, 149, 153, 157, 161, 165}
	}
	if ap.Channel > 0 {
		candidates = []int{ap.Channel}
	}
	legal := make([]int, 0, len(candidates))
	for _, ch := range candidates {
		a := ap
		a.Band = band
		a.Channel = ch
		a.ChannelMode = "fixed"
		if caps.Supports(a) == nil {
			legal = append(legal, ch)
		}
	}
	if len(legal) == 0 {
		return Selection{}, errors.New("no legal AP channel")
	}
	if len(neighbors) == 0 && len(busy) == 0 {
		for _, ch := range legal {
			if ch == previous {
				return Selection{Channel: ch, Reason: "last legal channel"}, nil
			}
		}
		return Selection{Channel: legal[0], Reason: "deterministic legal fallback"}, nil
	}
	best := Selection{Score: math.Inf(1)}
	for _, ch := range legal {
		score := ScoreChannel(ap, ch, neighbors, busy)
		if score < best.Score {
			best = Selection{Channel: ch, Reason: "survey and neighbor interference", Score: score}
		}
	}
	return best, nil
}

func ScoreChannel(ap config.WiFiAP, ch int, neighbors []Neighbor, busy map[int]float64) float64 {
	band := ap.Band
	if band == "" {
		band = "2g"
	}
	score := 50.0
	planAP := ap
	planAP.Channel = ch
	planAP.ChannelMode = "fixed"
	plan, planErr := config.PlanWiFiRadio(planAP)
	channels := []int{ch}
	if planErr == nil {
		channels = plan.Channels
	}
	measured, total := 0, 0.0
	for _, occupied := range channels {
		if value, ok := busy[occupied]; ok && value >= 0 && value <= 1 {
			measured++
			total += value
		}
	}
	if measured > 0 {
		score = total / float64(measured) * 100
	}
	for _, n := range neighbors {
		distance := math.Abs(float64(ch - n.Channel))
		weight := 0.0
		if band == "2g" {
			weight = math.Max(0, 1-distance/5)
		} else {
			for _, occupied := range channels {
				if occupied == n.Channel {
					weight = 1
					break
				}
			}
		}
		strength := math.Pow(10, float64(n.SignalDBm+90)/30)
		score += weight * strength * 12
	}
	if band == "2g" && ap.ChannelWidthMHz <= 20 && ch != 1 && ch != 6 && ch != 11 {
		score += 15
	}
	return score
}
