package core

// ParseClock reads a 24-hour "HH:MM" wall-clock time.
func ParseClock(value string) (hour, minute int, ok bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, false
	}
	digit := func(c byte) (int, bool) { return int(c - '0'), c >= '0' && c <= '9' }
	h1, ok1 := digit(value[0])
	h2, ok2 := digit(value[1])
	m1, ok3 := digit(value[3])
	m2, ok4 := digit(value[4])
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return 0, 0, false
	}
	hour, minute = h1*10+h2, m1*10+m2
	if hour > 23 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}
