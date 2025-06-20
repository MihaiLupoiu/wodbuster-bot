package utils

import (
	"testing"
)

func TestValidateEmail(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		wantErr bool
	}{
		{"valid email", "test@example.com", false},
		{"valid email with subdomain", "user@mail.example.com", false},
		{"valid email with numbers", "user123@example.com", false},
		{"empty email", "", true},
		{"whitespace only", "   ", true},
		{"invalid format - no @", "testexample.com", true},
		{"invalid format - no domain", "test@", true},
		{"invalid format - no username", "@example.com", true},
		{"invalid format - multiple @", "test@@example.com", true},
		{"invalid format - no TLD", "test@example", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEmail(tt.email)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEmail() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"valid password", "password123", false},
		{"minimum length", "1234", false},
		{"short but valid", "pass", false},
		{"empty password", "", true},
		{"whitespace only", "   ", true},
		{"too short", "123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePassword() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDay(t *testing.T) {
	tests := []struct {
		name    string
		day     string
		wantErr bool
	}{
		{"valid day - monday", "monday", false},
		{"valid day - Monday", "Monday", false},
		{"valid day - MONDAY", "MONDAY", false},
		{"valid day - tuesday", "tuesday", false},
		{"valid day - wednesday", "wednesday", false},
		{"valid day - thursday", "thursday", false},
		{"valid day - friday", "friday", false},
		{"valid day - saturday", "saturday", false},
		{"valid day - sunday", "sunday", false},
		{"empty day", "", true},
		{"whitespace only", "   ", true},
		{"invalid day", "funday", true},
		{"partial day", "mon", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDay(tt.day)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDay() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTime(t *testing.T) {
	tests := []struct {
		name    string
		timeStr string
		wantErr bool
	}{
		{"valid time - 10:00", "10:00", false},
		{"valid time - 09:30", "09:30", false},
		{"valid time - 23:59", "23:59", false},
		{"valid time - 00:00", "00:00", false},
		{"empty time", "", true},
		{"whitespace only", "   ", true},
		{"invalid format - no colon", "1000", true},
		{"invalid format - single digit hour", "9:00", true},
		{"invalid format - single digit minute", "10:0", true},
		{"invalid hour", "25:00", true},
		{"invalid minute", "10:60", true},
		{"invalid format - letters", "ab:cd", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTime(tt.timeStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTime() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateClassType(t *testing.T) {
	tests := []struct {
		name      string
		classType string
		wantErr   bool
	}{
		{"valid type - wod", "wod", false},
		{"valid type - WOD", "WOD", false},
		{"valid type - open", "open", false},
		{"valid type - strength", "strength", false},
		{"valid type - cardio", "cardio", false},
		{"valid type - yoga", "yoga", false},
		{"empty type", "", true},
		{"whitespace only", "   ", true},
		{"invalid type", "invalid", true},
		{"partial type", "wo", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClassType(tt.classType)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateClassType() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSanitizeInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"normal input", "hello", "hello"},
		{"input with spaces", "  hello  ", "hello"},
		{"input with null bytes", "hello\x00world", "helloworld"},
		{"empty input", "", ""},
		{"whitespace only", "   ", ""},
		{"mixed whitespace and content", "\t hello \n", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeInput(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeInput() = %v, want %v", result, tt.expected)
			}
		})
	}
}
