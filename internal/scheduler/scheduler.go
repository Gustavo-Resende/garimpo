package scheduler

import "time"

func IsWithinWindow(startHour, endHour int) bool {
	loc, err := time.LoadLocation("America/Bahia")
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	h := now.Hour()
	return h >= startHour && h < endHour
}
