package analytics

import "time"

// bucketLayout formats bucket starts as local wall time; it must match the to_char format in SQL.
const bucketLayout = "2006-01-02T15:04:05"

// truncate returns the start of the bucket containing t, in t's location. Weeks start on Monday
// (matching Postgres date_trunc).
func truncate(t time.Time, interval Interval) time.Time {
	y, m, d := t.Date()
	loc := t.Location()

	switch interval {
	case IntervalDay:
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	case IntervalWeek:
		offset := (int(t.Weekday()) + 6) % 7
		return time.Date(y, m, d-offset, 0, 0, 0, 0, loc)
	case IntervalMonth:
		return time.Date(y, m, 1, 0, 0, 0, 0, loc)
	case IntervalQuarter:
		return time.Date(y, ((m-1)/3)*3+1, 1, 0, 0, 0, 0, loc)
	default:
		return time.Date(y, 1, 1, 0, 0, 0, 0, loc)
	}
}

func next(t time.Time, interval Interval) time.Time {
	switch interval {
	case IntervalDay:
		return t.AddDate(0, 0, 1)
	case IntervalWeek:
		return t.AddDate(0, 0, 7)
	case IntervalMonth:
		return t.AddDate(0, 1, 0)
	case IntervalQuarter:
		return t.AddDate(0, 3, 0)
	default:
		return t.AddDate(1, 0, 0)
	}
}

// buckets returns the starts of all buckets overlapping [from, to) and the end of the last one.
func buckets(from, to time.Time, interval Interval) ([]time.Time, time.Time, error) {
	var out []time.Time
	b := truncate(from, interval)
	for ; b.Before(to); b = next(b, interval) {
		out = append(out, b)
		if len(out) > maxBuckets {
			return nil, time.Time{}, &ValidationError{"too many buckets; use a larger interval or a shorter range"}
		}
	}
	return out, b, nil
}
