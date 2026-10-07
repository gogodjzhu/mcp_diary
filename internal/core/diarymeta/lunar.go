package diarymeta

import (
	"time"

	lunarcal "github.com/6tail/lunar-go/calendar"
)

var weekdayNames = [...]string{"日", "一", "二", "三", "四", "五", "六"}

// LunarDate returns the Chinese lunar date for a YYYY-MM-DD date, e.g.
// "八月二十". ok is false when the date cannot be parsed.
func LunarDate(date string) (string, bool) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", false
	}
	lunar := lunarcal.NewSolarFromYmd(t.Year(), int(t.Month()), t.Day()).GetLunar()
	return lunar.GetMonthInChinese() + "月" + lunar.GetDayInChinese(), true
}

// WeekdayCN returns the Chinese weekday for a YYYY-MM-DD date, e.g. "三".
func WeekdayCN(date string) (string, bool) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", false
	}
	return weekdayNames[int(t.Weekday())], true
}
