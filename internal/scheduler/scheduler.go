package scheduler

import "time"

func IsWithinWindow(startHour, endHour int) bool {
	h := time.Now().Hour()
	return h >= startHour && h < endHour
}
