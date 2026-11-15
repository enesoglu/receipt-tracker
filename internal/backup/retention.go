package backup

import (
	"sort"
	"time"
)

// Expired keeps the latest dump on seven distinct UTC days, plus the latest
// dump in each of four ISO weeks. Unknown names and future dumps are untouched.
func Expired(objects []Object, now time.Time) []string {
	names := make([]string, 0, len(objects))
	for _, object := range objects {
		stamp, err := time.Parse(NameLayout, object.Name)
		if err == nil && !stamp.After(now.UTC()) {
			names = append(names, object.Name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	days := make(map[string]bool)
	weeks := make(map[[2]int]bool)
	expired := make([]string, 0)
	for _, name := range names {
		stamp, _ := time.Parse(NameLayout, name)
		day := stamp.Format("2006-01-02")
		year, week := stamp.ISOWeek()
		key := [2]int{year, week}
		keep := false
		if !days[day] && len(days) < 7 {
			days[day] = true
			keep = true
		}
		if !weeks[key] && len(weeks) < 4 {
			weeks[key] = true
			keep = true
		}
		if !keep {
			expired = append(expired, name)
		}
	}
	sort.Strings(expired)
	return expired
}
