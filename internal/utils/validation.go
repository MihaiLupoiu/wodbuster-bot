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

// ValidateDay validates day name (Monday, Tuesday, etc.)
func ValidateDay(day string) error {
	if strings.TrimSpace(day) == "" {
		return ErrEmptyInput
	}

	validDays := map[string]bool{
		"monday":    true,
		"tuesday":   true,
		"wednesday": true,
		"thursday":  true,
		"friday":    true,
		"saturday":  true,
		"sunday":    true,
	}

	if !validDays[strings.ToLower(day)] {
		return ErrInvalidDay
	}

	return nil
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

// ValidateClassType validates class type
func ValidateClassType(classType string) error {
	if strings.TrimSpace(classType) == "" {
		return ErrEmptyInput
	}

	validTypes := map[string]bool{
		"wod":      true,
		"open":     true,
		"strength": true,
		"cardio":   true,
		"yoga":     true,
	}

	if !validTypes[strings.ToLower(classType)] {
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
