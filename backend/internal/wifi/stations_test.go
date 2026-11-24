package wifi

import "testing"

func TestParseStationsKeepsAssociationMetrics(t *testing.T) {
	raw := "Station aa:bb:cc:dd:ee:ff (on wlan0)\n\tsignal: -54 dBm\n\tconnected time: 123 seconds\n\ttx bytes: 456\n\trx bytes: 789\n"
	stations, err := ParseStations(raw)
	if err != nil || len(stations) != 1 || stations[0].MAC != "aa:bb:cc:dd:ee:ff" || stations[0].SignalDBm != -54 || stations[0].ConnectedSeconds != 123 {
		t.Fatalf("%+v %v", stations, err)
	}
	if _, err = ParseStations("garbage"); err == nil {
		t.Fatal("malformed observation accepted as zero clients")
	}
	if stations, err = ParseStations(""); err != nil || len(stations) != 0 {
		t.Fatalf("empty station dump: %+v %v", stations, err)
	}
}
