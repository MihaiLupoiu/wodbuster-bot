package utils

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalidEmail     = errors.New("invalid email format")
	ErrInvalidDay       = errors.New("invalid day name")
	ErrInvalidTime      = errors.New("invalid time format")
	ErrEmptyInput       = errors.New("input cannot be empty")
	ErrInvalidClassType = errors.New("invalid class type")
)

// ValidateEmail validates email format using regex
func ValidateEmail(email string) error {
	if strings.TrimSpace(email) == "" {
		return ErrEmptyInput
	}

	emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	if !emailRegex.MatchString(email) {
		return ErrInvalidEmail
	}

	return nil
}

// ValidatePassword validates password requirements
func ValidatePassword(password string) error {
	if strings.TrimSpace(password) == "" {
		return ErrEmptyInput
	}

	// Basic password requirements - lenient since WODBuster requirements are unknown
	if len(password) < 4 {
		return fmt.Errorf("password must be at least 4 characters long")
	}

	return nil
}

var validDays = map[string]time.Weekday{
	"monday":    time.Monday,
	"tuesday":   time.Tuesday,
	"wednesday": time.Wednesday,
	"thursday":  time.Thursday,
	"friday":    time.Friday,
	"saturday":  time.Saturday,
	"sunday":    time.Sunday,
}

// ValidateDay validates day name (Monday, Tuesday, etc.)
func ValidateDay(day string) error {
	if strings.TrimSpace(day) == "" {
		return ErrEmptyInput
	}
	if _, ok := validDays[strings.ToLower(strings.TrimSpace(day))]; !ok {
		return ErrInvalidDay
	}
	return nil
}

// ParseWeekday turns a validated day name into a time.Weekday, so that the
// booking layer never has to parse a day a second time and disagree.
func ParseWeekday(day string) (time.Weekday, error) {
	wd, ok := validDays[strings.ToLower(strings.TrimSpace(day))]
	if !ok {
		return 0, ErrInvalidDay
	}
	return wd, nil
}

// ValidateTime validates time format (HH:MM)
func ValidateTime(timeStr string) error {
	if strings.TrimSpace(timeStr) == "" {
		return ErrEmptyInput
	}

	// Check format with regex first (exactly HH:MM)
	timeRegex := regexp.MustCompile(`^[0-2][0-9]:[0-5][0-9]$`)
	if !timeRegex.MatchString(timeStr) {
		return ErrInvalidTime
	}

	// Parse time in HH:MM format for additional validation
	_, err := time.Parse("15:04", timeStr)
	if err != nil {
		return ErrInvalidTime
	}

	return nil
}

// classNameRegex is what a WodBuster class name looks like: words, digits and
// a little punctuation ("Wod", "Open Box", "Hyrox", "Halterofilia +"). The
// boxes name their own classes, so this checks the shape and not a list; the
// run compares it, case-insensitively, against the names on the day.
var classNameRegex = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} .&+'/-]*$`)

const maxClassNameLen = 40

// ValidateClassType checks that a class name is plausible. It used to accept
// only wod, open, strength, cardio and yoga, which turned away real classes
// like Hyrox without the athlete knowing why.
func ValidateClassType(classType string) error {
	classType = strings.TrimSpace(classType)
	if classType == "" {
		return ErrEmptyInput
	}
	if len([]rune(classType)) > maxClassNameLen || !classNameRegex.MatchString(classType) {
		return ErrInvalidClassType
	}
	return nil
}

// SanitizeInput removes potentially dangerous characters
func SanitizeInput(input string) string {
	// Remove control characters and trim whitespace
	cleaned := strings.TrimSpace(input)

	// Remove any null bytes
	cleaned = strings.ReplaceAll(cleaned, "\x00", "")

	return cleaned
}
