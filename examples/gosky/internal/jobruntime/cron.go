package jobruntime

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Trigger types mirror SmartJobTriggerTypeEnum.
const (
	TriggerCron       = "cron"
	TriggerFixedDelay = "fixed_delay"
)

// schedule is one parsed six-field Spring-style cron expression:
// second minute hour day-of-month month day-of-week. It supports *,
// numeric lists/ranges/steps and JAN..DEC / SUN..SAT names; the special
// L/W/#/? characters are intentionally rejected.
type schedule struct {
	seconds  []int
	minutes  []int
	hours    []int
	days     []int // day of month 1..31; nil means wildcard
	months   []int // month 1..12; nil means wildcard
	weekdays []int // weekday 0..6 (Sunday=0); nil means wildcard
}

// ValidateCron reports whether the expression is accepted.
func ValidateCron(expression string) bool {
	_, err := parseCron(expression)
	return err == nil
}

// ValidateFixedDelay reports whether the trigger value is a positive
// integer number of seconds.
func ValidateFixedDelay(value string) bool {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	return err == nil && seconds > 0
}

// ParseFixedDelay returns the delay in seconds.
func ParseFixedDelay(value string) (int, error) {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("fixed delay must be a positive integer")
	}
	return seconds, nil
}

// NextCron returns up to num matching times strictly after from.
func NextCron(expression string, from time.Time, num int) ([]time.Time, error) {
	schedule, err := parseCron(expression)
	if err != nil {
		return nil, err
	}
	return schedule.next(from, num), nil
}

func parseCron(expression string) (*schedule, error) {
	fields := strings.Fields(strings.TrimSpace(expression))
	if len(fields) != 6 {
		return nil, fmt.Errorf("cron expression must have 6 fields")
	}
	seconds, err := parseField(fields[0], 0, 59, nil)
	if err != nil {
		return nil, fmt.Errorf("seconds: %w", err)
	}
	minutes, err := parseField(fields[1], 0, 59, nil)
	if err != nil {
		return nil, fmt.Errorf("minutes: %w", err)
	}
	hours, err := parseField(fields[2], 0, 23, nil)
	if err != nil {
		return nil, fmt.Errorf("hours: %w", err)
	}
	days, err := parseField(fields[3], 1, 31, nil)
	if err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	months, err := parseField(fields[4], 1, 12, monthNames)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	// Weekday accepts Spring's 0..7 convention where 0 and 7 both mean
	// Sunday; parseField bounds are 0..7 and values are normalized below.
	weekdays, err := parseField(fields[5], 0, 7, weekdayNames)
	if err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	weekdays = normalizeWeekdays(weekdays)
	return &schedule{
		seconds: seconds, minutes: minutes, hours: hours, days: days,
		months: months, weekdays: weekdays,
	}, nil
}

func normalizeWeekdays(values []int) []int {
	if values == nil {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value == 7 {
			value = 0
		}
		seen[value] = struct{}{}
	}
	result := make([]int, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sortInts(result)
	return result
}

var monthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var weekdayNames = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

// parseField parses one cron field, returning nil for the wildcard field.
func parseField(field string, min, max int, names map[string]int) ([]int, error) {
	if field == "*" || field == "?" {
		return nil, nil
	}
	if strings.ContainsAny(field, "LW#") {
		return nil, fmt.Errorf("unsupported cron special character")
	}
	values := make(map[int]struct{})
	for _, term := range strings.Split(field, ",") {
		step := 1
		rangePart := term
		if slash := strings.IndexByte(term, '/'); slash >= 0 {
			parsed, err := strconv.Atoi(term[slash+1:])
			if err != nil || parsed <= 0 {
				return nil, fmt.Errorf("invalid step %q", term)
			}
			step = parsed
			rangePart = term[:slash]
		}
		start, end, err := parseRange(rangePart, min, max, names)
		if err != nil {
			return nil, err
		}
		if end < start || start < min || end > max {
			return nil, fmt.Errorf("range out of bounds %q", term)
		}
		for value := start; value <= end; value += step {
			values[value] = struct{}{}
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("empty field")
	}
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sortInts(result)
	return result, nil
}

func parseRange(term string, min, max int, names map[string]int) (int, int, error) {
	if term == "*" {
		return min, max, nil
	}
	resolve := func(token string) (int, error) {
		token = strings.ToUpper(strings.TrimSpace(token))
		if names != nil {
			if value, ok := names[token]; ok {
				return value, nil
			}
		}
		value, err := strconv.Atoi(token)
		if err != nil {
			return 0, fmt.Errorf("invalid token %q", token)
		}
		return value, nil
	}
	if dash := strings.IndexByte(term, '-'); dash >= 0 {
		start, err := resolve(term[:dash])
		if err != nil {
			return 0, 0, err
		}
		end, err := resolve(term[dash+1:])
		if err != nil {
			return 0, 0, err
		}
		return start, end, nil
	}
	value, err := resolve(term)
	if err != nil {
		return 0, 0, err
	}
	return value, value, nil
}

// next walks candidate days and nested time fields. It searches at most
// 366 days ahead, which covers annual schedules including Feb 29.
func (s *schedule) next(from time.Time, num int) []time.Time {
	loc := from.Location()
	candidate := from.Add(time.Second).Truncate(time.Second)
	limit := from.AddDate(1, 0, 1)
	results := make([]time.Time, 0, num)

	day := time.Date(candidate.Year(), candidate.Month(), candidate.Day(), 0, 0, 0, 0, loc)
	for !day.After(limit) && len(results) < num {
		if s.dayMatches(day) {
			for _, hour := range s.hoursOrWild(23) {
				if len(results) == num {
					break
				}
				for _, minute := range s.fieldOrWild(s.minutes, 0, 59) {
					if len(results) == num {
						break
					}
					for _, second := range s.fieldOrWild(s.seconds, 0, 59) {
						t := day.Add(time.Duration(hour)*time.Hour +
							time.Duration(minute)*time.Minute +
							time.Duration(second)*time.Second)
						if t.After(from) && !t.Before(day) {
							results = append(results, t)
							if len(results) == num {
								break
							}
						}
					}
				}
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return results
}

func (s *schedule) hoursOrWild(max int) []int {
	return s.fieldOrWild(s.hours, 0, max)
}

func (s *schedule) fieldOrWild(values []int, min, max int) []int {
	if values != nil {
		return values
	}
	all := make([]int, 0, max-min+1)
	for i := min; i <= max; i++ {
		all = append(all, i)
	}
	return all
}

// dayMatches applies classic cron semantics: when both day-of-month and
// day-of-week are restricted, either matching is sufficient; when only one
// is restricted, it alone decides.
func (s *schedule) dayMatches(day time.Time) bool {
	if s.months != nil && !containsInt(s.months, int(day.Month())) {
		return false
	}
	domRestricted := s.days != nil
	dowRestricted := s.weekdays != nil
	domMatch := !domRestricted || containsInt(s.days, day.Day())
	weekday := int(day.Weekday())
	dowMatch := !dowRestricted || containsInt(s.weekdays, weekday)
	switch {
	case domRestricted && dowRestricted:
		return domMatch || dowMatch
	case domRestricted:
		return domMatch
	case dowRestricted:
		return dowMatch
	default:
		return true
	}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
