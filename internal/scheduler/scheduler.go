package scheduler

import "time"

func IsWithinWindow(startHour, endHour int) bool {
	loc := time.FixedZone("BRT", -3*60*60)
	now := time.Now().In(loc)
	h := now.Hour()
	return h >= startHour && h < endHour
}
