package wodbuster

import "time"

type ClassSchedule struct {
	Day       string
	Hour      string
	Available bool
	BookedBy  string
}

// UserSession represents a persistent browser session with WODBuster
// Email/password are stored in User model, not here
type UserSession struct {
	ChatID        int64           `bson:"chat_id" json:"chat_id"`
	Cookies       []SessionCookie `bson:"cookies" json:"cookies"`
	LastLoginTime time.Time       `bson:"last_login_time" json:"last_login_time"`
	ExpiresAt     time.Time       `bson:"expires_at" json:"expires_at"`
	IsValid       bool            `bson:"is_valid" json:"is_valid"`
	CreatedAt     time.Time       `bson:"created_at" json:"created_at"`
	UpdatedAt     time.Time       `bson:"updated_at" json:"updated_at"`
}

// SessionCookie represents a browser cookie for session persistence
type SessionCookie struct {
	Name     string    `bson:"name" json:"name"`
	Value    string    `bson:"value" json:"value"`
	Domain   string    `bson:"domain" json:"domain"`
	Path     string    `bson:"path" json:"path"`
	Expires  time.Time `bson:"expires" json:"expires"`
	Secure   bool      `bson:"secure" json:"secure"`
	HttpOnly bool      `bson:"http_only" json:"http_only"`
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
