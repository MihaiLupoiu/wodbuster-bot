package models

import "time"

type User struct {
	ChatID                int64                  `json:"chat_id" bson:"chat_id"`
	IsAuthenticated       bool                   `json:"is_authenticated" bson:"is_authenticated"`
	Email                 string                 `json:"email" bson:"email"`
	Password              string                 `json:"password" bson:"password"`
	ClassBookingSchedules []ClassBookingSchedule `json:"class_booking_schedules" bson:"class_booking_schedules"`

	// No session is stored. A WodBuster session is a session cookie with no
	// expiry of its own, dropped by the server after a while idle, so one
	// saved on Monday is worthless by Sunday. Every run logs in again, which
	// is why the (encrypted) password is what gets kept.
	LastLoginTime time.Time `json:"last_login_time,omitempty" bson:"last_login_time,omitempty"`
	CreatedAt     time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" bson:"updated_at"`
}

type ClassBookingSchedule struct {
	ID        string `json:"id" bson:"id"`                 // Unique identifier for the class booking
	ClassType string `json:"class_type" bson:"class_type"` // e.g., "WOD", "Open"
	Day       string `json:"day" bson:"day"`               // e.g., "Monday", "Tuesday"
	Hour      string `json:"hour" bson:"hour"`             // e.g., "10:00"
}

// BookingAttempt tracks booking attempts - NO sensitive data stored here
// Use ChatID to lookup user credentials from User model when needed
type BookingAttempt struct {
	ID          string    `bson:"_id" json:"id"`
	ChatID      int64     `bson:"chat_id" json:"chat_id"` // Only reference to user
	Day         string    `bson:"day" json:"day"`
	Hour        string    `bson:"hour" json:"hour"`
	ClassType   string    `bson:"class_type" json:"class_type"`
	Status      string    `bson:"status" json:"status"` // pending, success, failed, expired
	AttemptTime time.Time `bson:"attempt_time" json:"attempt_time"`
	ErrorMsg    string    `bson:"error_msg,omitempty" json:"error_msg,omitempty"`
	RetryCount  int       `bson:"retry_count" json:"retry_count"`
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`
}

// BookingWindow represents when booking becomes available
type BookingWindow struct {
	Day           string        `json:"day"`
	Hour          string        `json:"hour"`
	ClassType     string        `json:"class_type"`
	OpensAt       time.Time     `json:"opens_at"`       // When booking opens
	TimeRemaining time.Duration `json:"time_remaining"` // Time until booking opens
	IsOpen        bool          `json:"is_open"`        // Whether booking is currently open
}
