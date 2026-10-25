package server

import "testing"

func TestNewLogger(t *testing.T) {
	for _, format := range []string{"console", "json"} {
		for _, level := range []string{"debug", "info", "warn", "error"} {
			if _, err := newLogger(format, level); err != nil {
				t.Errorf("%s/%s: %v", format, level, err)
			}
		}
	}
	for _, c := range [][2]string{{"text", "info"}, {"", "info"}, {"json", "loud"}, {"json", ""}, {"json", "fatal"}} {
		if _, err := newLogger(c[0], c[1]); err == nil {
			t.Errorf("%s/%s should be rejected", c[0], c[1])
		}
	}
}
