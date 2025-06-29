package models

import (
	"net/http"
	"time"
)

type User struct {
	ChatID                int64                  `json:"chat_id" bson:"chat_id"`
	IsAuthenticated       bool                   `json:"is_authenticated" bson:"is_authenticated"`
	Email                 string                 `json:"email" bson:"email"`
	Password              string                 `json:"password" bson:"password"`
	ClassBookingSchedules []ClassBookingSchedule `json:"class_booking_schedules" bson:"class_booking_schedules"`
	// Session data - simplified to store only the essential WODBuster session cookie
	WODBusterSessionCookie *http.Cookie `json:"wodbuster_session_cookie,omitempty" bson:"wodbuster_session_cookie,omitempty"`
	SessionExpiresAt       time.Time    `json:"session_expires_at,omitempty" bson:"session_expires_at,omitempty"`
	SessionValid           bool         `json:"session_valid" bson:"session_valid"`
	LastLoginTime          time.Time    `json:"last_login_time,omitempty" bson:"last_login_time,omitempty"`
	CreatedAt              time.Time    `json:"created_at" bson:"created_at"`
	UpdatedAt              time.Time    `json:"updated_at" bson:"updated_at"`
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

// Helper methods for session management
func (u *User) HasValidSession() bool {
	return u.SessionValid &&
		u.WODBusterSessionCookie != nil &&
		time.Now().Before(u.SessionExpiresAt)
}

func (u *User) UpdateSession(sessionCookie *http.Cookie) {
	u.WODBusterSessionCookie = sessionCookie
	u.LastLoginTime = time.Now()
	u.SessionExpiresAt = sessionCookie.Expires
	u.SessionValid = true
	u.UpdatedAt = time.Now()
}

func (u *User) ClearSession() {
	u.WODBusterSessionCookie = nil
	u.SessionValid = false
	u.UpdatedAt = time.Now()
}
